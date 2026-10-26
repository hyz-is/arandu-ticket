package ticket

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/filesystem"

	"github.com/hyz-is/arandu-ticket/internal/platform"
)

// TeamService orchestrates what the platform team does with the tickets of every
// space.
//
// A member of the team is a subject of Config.TeamTenant, and TeamPolicy
// decides what they may do. The tickets are in the customers' tenants, so a use
// case here goes in two steps: it asks the policy for a Grant in the team's own
// realm, reads the list or finds the ticket's tenant with it, and then acts
// with a Grant scoped to that one tenant, which exists only because the first
// one was issued for the same action. Nothing a request names chooses the
// tenant: it is the one the ticket's row says.
type TeamService struct {
	*core
	policy TeamPolicy
}

// NewTeamService wires the service over the application's database handle.
func NewTeamService(db *data.DB, cfg Config) *TeamService {
	cfg = cfg.withDefaults()
	return &TeamService{core: newCore(db, cfg), policy: TeamPolicy{Realm: cfg.TeamTenant}}
}

// sortableTeam is the ordering allowlist of the team's list.
var sortableTeam = map[string]string{
	"": "updated_at", "updated_at": "updated_at", "created_at": "created_at", "number": "number",
	"subject": "subject", "status": "status", "requester": "requester_name", "assignee": "assignee_name",
	"tenant": "tenant_id", "priority": "priority",
}

// priorityOrder sorts the priorities by how urgent they are and not by how
// they spell.
const priorityOrder = "CASE priority WHEN 'urgent' THEN 3 WHEN 'high' THEN 2 WHEN 'normal' THEN 1 ELSE 0 END"

// List returns a page of the tickets of every space: searched by number,
// subject and requester, filtered by status, priority, assignee and space, and
// ordered by the server.
func (s *TeamService) List(ctx context.Context, actor security.Subject, q TeamQuery) (Page, error) {
	g, err := security.Authorize(ctx, s.policy, actor, TicketViewAll, Ticket{})
	if err != nil {
		return Page{}, err
	}
	column, ok := sortableTeam[q.Sort]
	if !ok {
		return Page{}, s.labels.invalid("sort", "sort")
	}
	statuses, err := s.statuses(q.Statuses)
	if err != nil {
		return Page{}, err
	}
	priorities := make([]any, 0, len(q.Priorities))
	for _, p := range q.Priorities {
		if !p.Valid() {
			return Page{}, s.labels.invalid("priority", "priority")
		}
		priorities = append(priorities, string(p))
	}
	if q.Tenant != "" && !security.ValidTenant(q.Tenant) {
		return Page{}, s.labels.invalid("tenant", "tenant")
	}
	perPage, page := s.paging(q.PerPage, q.Page)

	filtered := func() *platform.TicketQuery {
		b := platform.Tickets(s.db)
		if len(statuses) > 0 {
			b = b.WhereIn("status", statuses)
		}
		if len(priorities) > 0 {
			b = b.WhereIn("priority", priorities)
		}
		switch q.Assignee {
		case "":
		case AssigneeNone:
			b = b.Where("assignee_id", "=", "")
		default:
			b = b.Where("assignee_id", "=", q.Assignee)
		}
		if q.Tenant != "" {
			b = b.Where("tenant_id", "=", q.Tenant)
		}
		matching(b.Base(), q.Search, "subject", "requester_name", "requester_email", "category")
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page{}, err
	}
	rows := filtered()
	switch {
	case column == "priority":
		direction := "DESC"
		if q.Dir == "asc" {
			direction = "ASC"
		}
		rows.Base().GetQuery().OrderByRaw(priorityOrder + " " + direction)
	case q.Dir == "asc":
		rows = rows.OrderBy(column)
	default:
		rows = rows.OrderByDesc(column)
	}
	found, err := rows.OrderByDesc("id").Offset((page-1)*perPage).Limit(perPage).Get(ctx, g, platform.Columns...)
	if err != nil {
		return Page{}, err
	}
	out := Page{Rows: make([]Summary, 0, len(found)), Total: int(total), Page: page, Pages: pagesOf(int(total), perPage)}
	for _, t := range found {
		out.Rows = append(out.Rows, summaryOfPlatform(t))
	}
	return out, nil
}

// Find returns one conversation with its internal notes: the team reads it all.
func (s *TeamService) Find(ctx context.Context, actor security.Subject, id string) (Conversation, error) {
	t, g, err := s.reach(ctx, actor, TicketViewAll, id)
	if err != nil {
		return Conversation{}, err
	}
	return s.conversation(ctx, g, t, true)
}

// Team returns the members of the team a ticket can be assigned to, as the
// application's Directory lists them. Without a Directory it is empty.
func (s *TeamService) Team(ctx context.Context, actor security.Subject) ([]Person, error) {
	if _, err := security.Authorize(ctx, s.policy, actor, TicketViewAll, Ticket{}); err != nil {
		return nil, err
	}
	if s.cfg.Directory == nil {
		return nil, nil
	}
	return s.cfg.Directory.Team(ctx)
}

// Reply answers the customer. The ticket waits for them, and they are told. A
// closed ticket takes no reply.
func (s *TeamService) Reply(ctx context.Context, actor security.Subject, id string, in ReplyRequest, uploads []Upload) (*Message, error) {
	if errs := s.validateReply(in, len(uploads)); errs.Any() {
		return nil, errs
	}
	t, g, err := s.reach(ctx, actor, TicketManage, id)
	if err != nil {
		return nil, err
	}
	if !t.CanReply() {
		return nil, s.labels.invalid("body", "ticket_closed")
	}
	who := s.person(ctx, s.cfg.TeamTenant, actor.ID)
	reply, err := s.add(ctx, g, t, message{
		Role: RoleTeam, AuthorID: actor.ID, AuthorName: who.Name, Body: trimmed(in.Body), Uploads: uploads,
	}, t.ReceiveTeamReply)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketReplied, t, map[string]any{"role": string(RoleTeam), "files": len(reply.files)}); err != nil {
		return nil, err
	}
	s.notify(ctx, EventTeamReplied, summaryOf(t), messageViewOf(reply.message, reply.files))
	return reply.message, nil
}

// Note writes a note only the team reads. It changes neither the status nor
// the moment of the last message, tells nobody, and its entry in the log says
// that a note was written and not what it says.
func (s *TeamService) Note(ctx context.Context, actor security.Subject, id string, in ReplyRequest, uploads []Upload) (*Message, error) {
	if errs := s.validateReply(in, len(uploads)); errs.Any() {
		return nil, errs
	}
	t, g, err := s.reach(ctx, actor, TicketNote, id)
	if err != nil {
		return nil, err
	}
	who := s.person(ctx, s.cfg.TeamTenant, actor.ID)
	note, err := s.add(ctx, g, t, message{
		Role: RoleTeam, AuthorID: actor.ID, AuthorName: who.Name, Body: trimmed(in.Body), Internal: true, Uploads: uploads,
	}, t.AddNote)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketNoteAdded, t, map[string]any{"files": len(note.files)}); err != nil {
		return nil, err
	}
	return note.message, nil
}

// SetStatus moves a ticket to the status the team chose. Closing it and taking
// it out of closed have entries of their own in the log.
func (s *TeamService) SetStatus(ctx context.Context, actor security.Subject, id string, next Status) (*Ticket, error) {
	if !next.Valid() {
		return nil, s.labels.invalid("status", "status")
	}
	t, g, err := s.reach(ctx, actor, TicketManage, id)
	if err != nil {
		return nil, err
	}
	from := t.Status
	if err := t.SetStatus(next, s.now()); err != nil {
		return nil, s.labels.invalid("status", "same_status")
	}
	if _, err := t.Save(ctx, g); err != nil {
		return nil, err
	}
	event := ActivityTicketStatusChanged
	switch {
	case next == StatusClosed:
		event = ActivityTicketClosed
	case from == StatusClosed:
		event = ActivityTicketReopened
	}
	if err := s.record(ctx, g, actor, event, t, map[string]any{"from": string(from), "to": string(next), "side": "team"}); err != nil {
		return nil, err
	}
	return t, nil
}

// SetPriority changes how soon the team looks at a ticket.
func (s *TeamService) SetPriority(ctx context.Context, actor security.Subject, id string, next Priority) (*Ticket, error) {
	if !next.Valid() {
		return nil, s.labels.invalid("priority", "priority")
	}
	t, g, err := s.reach(ctx, actor, TicketManage, id)
	if err != nil {
		return nil, err
	}
	from := t.Priority
	if err := t.SetPriority(next, s.now()); err != nil {
		return nil, s.labels.invalid("priority", "same_priority")
	}
	if _, err := t.Save(ctx, g); err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketPriorityChanged, t, map[string]any{"from": string(from), "to": string(next)}); err != nil {
		return nil, err
	}
	return t, nil
}

// Assign gives a ticket to a member of the team, and an empty identifier takes
// it back. With a Directory the member has to be on its list; without one a
// member can only take a ticket themselves.
func (s *TeamService) Assign(ctx context.Context, actor security.Subject, id, assigneeID string) (*Ticket, error) {
	assigneeID = trimmed(assigneeID)
	t, g, err := s.reach(ctx, actor, TicketAssign, id)
	if err != nil {
		return nil, err
	}
	name := ""
	if assigneeID != "" {
		member, err := s.member(ctx, actor, assigneeID)
		if err != nil {
			return nil, err
		}
		name = member.Name
	}
	from := t.AssigneeID
	if err := t.Assign(assigneeID, name, s.now()); err != nil {
		return nil, s.labels.invalid("assignee", "same_assignee")
	}
	if _, err := t.Save(ctx, g); err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketAssigned, t, map[string]any{"from": from, "to": assigneeID}); err != nil {
		return nil, err
	}
	return t, nil
}

// member is the member of the team a ticket is given to.
func (s *TeamService) member(ctx context.Context, actor security.Subject, id string) (Person, error) {
	if s.cfg.Directory == nil {
		if id != actor.ID {
			return Person{}, s.labels.invalid("assignee", "assignee_self")
		}
		return Person{ID: id}, nil
	}
	team, err := s.cfg.Directory.Team(ctx)
	if err != nil {
		return Person{}, err
	}
	for _, member := range team {
		if member.ID == id {
			member.Name = clip(trimmed(member.Name), maxNameLen)
			return member, nil
		}
	}
	return Person{}, s.labels.invalid("assignee", "assignee_unknown")
}

// Attachment opens a file of any message of a ticket, the team's notes
// included. The caller closes the body. Reading it is recorded.
func (s *TeamService) Attachment(ctx context.Context, actor security.Subject, ticketID, attachmentID string) (*Attachment, filesystem.File, error) {
	t, g, err := s.reach(ctx, actor, TicketViewAll, ticketID)
	if err != nil {
		return nil, filesystem.File{}, err
	}
	return s.open(ctx, g, actor, t, attachmentID, true)
}

// reach finds the ticket a team member acts on and the Grant to act on it with.
//
// The policy is asked twice, as it is for a customer's reading: first for the
// kind of act, with nothing in hand, and then about the ticket itself. Between
// them the row is found in any tenant -- through the Global table, with the
// realm's Grant -- and its tenant is the one the work is scoped to.
func (s *TeamService) reach(ctx context.Context, actor security.Subject, a security.Action, id string) (*Ticket, security.Grant, error) {
	realm, err := security.Authorize(ctx, s.policy, actor, a, Ticket{})
	if err != nil {
		return nil, security.Grant{}, err
	}
	row, err := platform.Tickets(s.db).WhereKey(id).First(ctx, realm, "id", "tenant_id")
	if err != nil {
		return nil, security.Grant{}, err
	}
	if row == nil {
		return nil, security.Grant{}, ErrNotFound
	}
	g, err := scopedGrant(realm, a, row.TenantID)
	if err != nil {
		return nil, security.Grant{}, err
	}
	t, err := Tickets(s.db).WhereKey(id).First(ctx, g)
	if err != nil {
		return nil, security.Grant{}, err
	}
	if t == nil {
		return nil, security.Grant{}, ErrNotFound
	}
	if _, err := security.Authorize(ctx, s.policy, actor, a, *t); err != nil {
		return nil, security.Grant{}, err
	}
	return t, g, nil
}
