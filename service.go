package ticket

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/filesystem"
)

// core is what the two services share: the database handle, the configuration,
// the sentences and the clock. It holds no policy, because each service has its
// own, and nothing here authorizes anything: a method of core is reached only
// after the service that called it asked its policy and holds the Grant.
type core struct {
	db     *data.DB
	cfg    Config
	labels Labels
	now    clock
}

func newCore(db *data.DB, cfg Config) *core {
	read := cfg.Now
	return &core{db: db, cfg: cfg, labels: labelsOf(cfg), now: func() time.Time { return read().UTC() }}
}

// TicketService orchestrates what a customer does with tickets: it validates
// the request, asks the policy for a Grant, applies the rules the entity holds
// for itself, and persists with that Grant.
//
// It receives its collaborators through the constructor. There is no container
// and no resolution by reflection: what this service is made of is written at
// the one place that builds it, and reading that place is how somebody learns
// what the package touches.
//
// Everything a handler is allowed to do for a customer goes through here. The
// service is the only owner of the database handle on this side, so the request
// layer cannot reach a Model before the policy has answered.
type TicketService struct {
	*core
	policy TicketPolicy
}

// NewTicketService wires the service over the application's database handle.
func NewTicketService(db *data.DB, cfg Config) *TicketService {
	return &TicketService{core: newCore(db, cfg.withDefaults()), policy: TicketPolicy{}}
}

// viewer is how much of the space a customer reads.
type viewer int

const (
	// viewsOwn reads the tickets the subject asked for.
	viewsOwn viewer = iota
	// viewsSpace reads every ticket of the space.
	viewsSpace
)

// action is the action the viewer was authorized with.
func (v viewer) action() security.Action {
	if v == viewsSpace {
		return TicketViewSpace
	}
	return TicketViewOwn
}

// authorizeView asks the policy for the widest reading the subject holds: the
// whole space's, else their own. Both questions go to the policy; the answer
// says which one it allowed, and the statement is narrowed to it.
func (s *TicketService) authorizeView(ctx context.Context, actor security.Subject) (security.Grant, viewer, error) {
	g, err := security.Authorize(ctx, s.policy, actor, TicketViewSpace, Ticket{})
	if err == nil {
		return g, viewsSpace, nil
	}
	g, ownErr := security.Authorize(ctx, s.policy, actor, TicketViewOwn, Ticket{})
	if ownErr != nil {
		return security.Grant{}, viewsOwn, ownErr
	}
	return g, viewsOwn, nil
}

// visible narrows a statement to what the viewer reads: every ticket of the
// space, or the subject's own. The tenant is the Grant's, applied by the
// table; this is the second predicate, and the one a rule about individual
// records belongs in.
func (v viewer) visible(q *TicketQuery, actor security.Subject) *TicketQuery {
	if v == viewsOwn {
		return q.Where("requester_id", "=", actor.ID)
	}
	return q
}

// sortableTicket is the ordering allowlist of the customer's list. A column
// name taken directly from a request would turn ordering into an injection
// surface.
var sortableTicket = map[string]string{
	"": "updated_at", "updated_at": "updated_at", "created_at": "created_at", "number": "number",
	"subject": "subject", "status": "status", "category": "category",
}

// List returns a page of the customer's tickets: their own, or the whole
// space's when the policy allows it, searched, filtered by status and ordered
// by the server.
func (s *TicketService) List(ctx context.Context, actor security.Subject, q ListQuery) (Page, error) {
	g, scope, err := s.authorizeView(ctx, actor)
	if err != nil {
		return Page{}, err
	}
	column, ok := sortableTicket[q.Sort]
	if !ok {
		return Page{}, s.labels.invalid("sort", "sort")
	}
	statuses, err := s.statuses(q.Statuses)
	if err != nil {
		return Page{}, err
	}
	perPage, page := s.paging(q.PerPage, q.Page)

	filtered := func() *TicketQuery {
		b := scope.visible(Tickets(s.db), actor)
		if len(statuses) > 0 {
			b = b.WhereIn("status", statuses)
		}
		matching(b.Base(), q.Search, "subject", "category")
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page{}, err
	}
	rows := filtered()
	if q.Dir == "asc" {
		rows = rows.OrderBy(column)
	} else {
		rows = rows.OrderByDesc(column)
	}
	found, err := rows.OrderByDesc("id").Offset((page-1)*perPage).Limit(perPage).Get(ctx, g)
	if err != nil {
		return Page{}, err
	}
	out := Page{Rows: make([]Summary, 0, len(found)), Total: int(total), Page: page, Pages: pagesOf(int(total), perPage)}
	for _, t := range found {
		out.Rows = append(out.Rows, summaryOf(t))
	}
	return out, nil
}

// Find returns one conversation: the ticket and the messages the customer may
// read. An internal note is not among them, and nothing says one exists. A
// ticket of another space, and one of the same space the subject may not read,
// are not found.
func (s *TicketService) Find(ctx context.Context, actor security.Subject, id string) (Conversation, error) {
	g, scope, err := s.authorizeView(ctx, actor)
	if err != nil {
		return Conversation{}, err
	}
	t, err := s.seen(ctx, g, scope, actor, id)
	if err != nil {
		return Conversation{}, err
	}
	return s.conversation(ctx, g, t, false)
}

// seen loads a ticket the viewer reads, and asks the policy a second time
// about the row itself.
//
// The first Authorize saw an empty ticket, so every rule about the record --
// whose it is -- never ran. A ticket the policy refuses on the second call is
// answered as not found, as a ticket of another space is: telling a customer
// that a ticket exists and is not theirs tells them what exists.
func (s *TicketService) seen(ctx context.Context, g security.Grant, scope viewer, actor security.Subject, id string) (*Ticket, error) {
	t, err := scope.visible(Tickets(s.db).WhereKey(id), actor).First(ctx, g)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}
	if _, err := security.Authorize(ctx, s.policy, actor, scope.action(), *t); err != nil {
		return nil, ErrNotFound
	}
	return t, nil
}

// Open opens a ticket for the signed-in customer, in their own space, with the
// first message and the files that came with it.
func (s *TicketService) Open(ctx context.Context, actor security.Subject, in OpenRequest, uploads []Upload) (*Ticket, error) {
	if errs := s.validateOpen(in, len(uploads)); errs.Any() {
		return nil, errs
	}
	proposed := Ticket{Subject: trimmed(in.Subject)}
	g, err := security.Authorize(ctx, s.policy, actor, TicketCreate, proposed)
	if err != nil {
		return nil, err
	}
	who := s.person(ctx, data.Tenant(g), actor.ID)
	created, err := s.create(ctx, g, draft{
		Subject: trimmed(in.Subject), Category: trimmed(in.Category), Body: trimmed(in.Body),
		Origin: OriginWeb, Requester: who, Uploads: uploads,
	})
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketOpened, created.ticket, map[string]any{"origin": string(OriginWeb), "files": len(created.files)}); err != nil {
		return nil, err
	}
	s.notify(ctx, EventOpened, summaryOf(created.ticket), messageViewOf(created.message, created.files))
	return created.ticket, nil
}

// Reply adds the customer's message to a ticket. A ticket nobody answered yet
// stays open; any other waits for the team. A closed ticket takes no reply: it
// is reopened first.
func (s *TicketService) Reply(ctx context.Context, actor security.Subject, id string, in ReplyRequest, uploads []Upload) (*Message, error) {
	if errs := s.validateReply(in, len(uploads)); errs.Any() {
		return nil, errs
	}
	g, err := security.Authorize(ctx, s.policy, actor, TicketReply, Ticket{})
	if err != nil {
		return nil, err
	}
	t, err := s.owned(ctx, g, actor, TicketReply, id)
	if err != nil {
		return nil, err
	}
	if !t.CanReply() {
		return nil, s.labels.invalid("body", "ticket_closed")
	}
	who := s.person(ctx, data.Tenant(g), actor.ID)
	reply, err := s.add(ctx, g, t, message{
		Role: RoleCustomer, AuthorID: actor.ID, AuthorName: who.Name, Body: trimmed(in.Body), Uploads: uploads,
	}, t.ReceiveCustomerReply)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketReplied, t, map[string]any{"role": string(RoleCustomer), "files": len(reply.files)}); err != nil {
		return nil, err
	}
	s.notify(ctx, EventCustomerReplied, summaryOf(t), messageViewOf(reply.message, reply.files))
	return reply.message, nil
}

// Close closes a ticket the customer may read.
func (s *TicketService) Close(ctx context.Context, actor security.Subject, id string) (*Ticket, error) {
	g, err := security.Authorize(ctx, s.policy, actor, TicketClose, Ticket{})
	if err != nil {
		return nil, err
	}
	t, err := s.owned(ctx, g, actor, TicketClose, id)
	if err != nil {
		return nil, err
	}
	from := t.Status
	if err := t.Close(s.now()); err != nil {
		return nil, s.labels.invalid("status", "already_closed")
	}
	if _, err := t.Save(ctx, g); err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketClosed, t, map[string]any{"from": string(from), "to": string(t.Status)}); err != nil {
		return nil, err
	}
	return t, nil
}

// Reopen brings a resolved or closed ticket back to the team.
func (s *TicketService) Reopen(ctx context.Context, actor security.Subject, id string) (*Ticket, error) {
	g, err := security.Authorize(ctx, s.policy, actor, TicketClose, Ticket{})
	if err != nil {
		return nil, err
	}
	t, err := s.owned(ctx, g, actor, TicketClose, id)
	if err != nil {
		return nil, err
	}
	from := t.Status
	if err := t.Reopen(s.now()); err != nil {
		return nil, s.labels.invalid("status", "not_closed")
	}
	if _, err := t.Save(ctx, g); err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, ActivityTicketReopened, t, map[string]any{"from": string(from), "to": string(t.Status)}); err != nil {
		return nil, err
	}
	return t, nil
}

// owned loads a ticket the customer acts on and asks the policy about the row.
// The read is narrowed to the tickets the subject can see, so a ticket of
// another requester is not found for one who reads only their own.
func (s *TicketService) owned(ctx context.Context, g security.Grant, actor security.Subject, a security.Action, id string) (*Ticket, error) {
	scope := viewsOwn
	if actor.Can(TicketViewSpace) {
		scope = viewsSpace
	}
	t, err := scope.visible(Tickets(s.db).WhereKey(id), actor).First(ctx, g)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}
	if _, err := security.Authorize(ctx, s.policy, actor, a, *t); err != nil {
		return nil, ErrNotFound
	}
	return t, nil
}

// Attachment opens a file of a message the customer may read. The caller
// closes the body. Reading it is recorded.
func (s *TicketService) Attachment(ctx context.Context, actor security.Subject, ticketID, attachmentID string) (*Attachment, filesystem.File, error) {
	g, scope, err := s.authorizeView(ctx, actor)
	if err != nil {
		return nil, filesystem.File{}, err
	}
	t, err := s.seen(ctx, g, scope, actor, ticketID)
	if err != nil {
		return nil, filesystem.File{}, err
	}
	return s.open(ctx, g, actor, t, attachmentID, false)
}

// statuses turns the statuses a request named into the values a statement
// compares with, refusing one that is not a status.
func (c *core) statuses(in []Status) ([]any, error) {
	out := make([]any, 0, len(in))
	for _, s := range in {
		if !s.Valid() {
			return nil, c.labels.invalid("status", "status")
		}
		out = append(out, string(s))
	}
	return out, nil
}

// paging bounds a page: from one, and at most PageSize rows unless the caller
// asked for fewer.
func (c *core) paging(perPage, page int) (int, int) {
	if perPage <= 0 || perPage > MaxPageSize {
		perPage = c.cfg.PageSize
	}
	if page < 1 {
		page = 1
	}
	return perPage, page
}

// matching adds one group, column LIKE %term% OR ..., case insensitive on every
// engine, to a statement. The term is bound, never written into the statement;
// a % or _ in it widens the match and nothing more. A term that reads as a
// ticket number -- "123", "#000123" -- also matches that number. An empty term
// adds nothing.
func matching(b *model.Builder, term string, columns ...string) {
	term = strings.TrimSpace(term)
	if term == "" {
		return
	}
	pattern := "%" + term + "%"
	number, isNumber := ticketNumber(term)
	b.WhereGroup("and", func(nested *model.Builder) {
		for i, column := range columns {
			boolean := "or"
			if i == 0 {
				boolean = "and"
			}
			nested.GetQuery().WhereLike(column, pattern, false, boolean, false)
		}
		if isNumber {
			nested.GetQuery().OrWhere("number", "=", number)
		}
	})
}

// ticketNumber reads "#000123", "000123" and "123" as 123.
func ticketNumber(term string) (int64, bool) {
	term = strings.TrimPrefix(strings.TrimSpace(term), "#")
	if term == "" || len(term) > 12 {
		return 0, false
	}
	n, err := strconv.ParseInt(term, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// validateBody checks the text of a message. A message needs words, or a file
// to go with it.
func (c *core) validateBody(errs validation.Errors, body string, files int) {
	body = trimmed(body)
	switch {
	case body == "" && files == 0:
		errs.Add("body", c.labels.message("required"))
	case utf8.RuneCountInString(body) > MaxBodyChars:
		errs.Add("body", c.labels.message("max", map[string]any{"max": MaxBodyChars}))
	}
}

func (c *core) validateOpen(in OpenRequest, files int) validation.Errors {
	errs := validation.Errors{}
	subject := trimmed(in.Subject)
	switch {
	case subject == "":
		errs.Add("subject", c.labels.message("required"))
	case utf8.RuneCountInString(subject) > maxSubjectLen:
		errs.Add("subject", c.labels.message("max", map[string]any{"max": maxSubjectLen}))
	}
	if utf8.RuneCountInString(trimmed(in.Category)) > maxCategoryLen {
		errs.Add("category", c.labels.message("max", map[string]any{"max": maxCategoryLen}))
	}
	c.validateBody(errs, in.Body, files)
	return errs
}

func (c *core) validateReply(in ReplyRequest, files int) validation.Errors {
	errs := validation.Errors{}
	c.validateBody(errs, in.Body, files)
	return errs
}

// person is who a user is, as the application's Directory knows them; just the
// identifier when it has none or does not know them.
func (c *core) person(ctx context.Context, tenant, id string) Person {
	who := Person{ID: id}
	if c.cfg.Directory == nil || id == "" {
		return who
	}
	known, err := c.cfg.Directory.Person(ctx, tenant, id)
	if err != nil {
		return who
	}
	who.Name = clip(trimmed(known.Name), maxNameLen)
	who.Email = clip(trimmed(known.Email), maxEmailLen)
	return who
}

// clip cuts text to at most n characters.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}

// CanOpen answers whether the subject may open a ticket, for the screen that
// draws the form. It asks the policy exactly as Open does, and the refusal it
// returns is the one Open would.
func (s *TicketService) CanOpen(ctx context.Context, actor security.Subject) error {
	_, err := security.Authorize(ctx, s.policy, actor, TicketCreate, Ticket{})
	return err
}
