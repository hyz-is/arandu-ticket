package ticket

import (
	"html/template"
	"net/url"
	"strings"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/kyse/components"
)

// This file builds what the screens are handed. Like pages.go it reads nothing
// and decides nothing: the handler already asked the service, and these only
// ask the policy what to draw -- a button for an act the subject may do -- never
// whether to do it.

// recordOf is a ticket as the policy is asked about it: the fields it decides
// on, and no connection.
func recordOf(t Summary) Ticket {
	return Ticket{ID: t.ID, TenantID: t.Tenant, RequesterID: t.RequesterID, Status: t.Status}
}

// allows says whether a customer may do an act on a ticket, to decide whether a
// button is drawn. It cannot authorize anything: it throws the Grant away, and
// the route behind the button asks the policy again.
func (m *Module) allows(ctx *fhttp.Context, who security.Subject, a security.Action, record Ticket) bool {
	return auth.Allows(ctx.Ctx(), TicketPolicy{}, who, a, record)
}

// teamAllows is allows for a member of the team.
func (m *Module) teamAllows(ctx *fhttp.Context, who security.Subject, a security.Action) bool {
	return auth.Allows(ctx.Ctx(), TeamPolicy{Realm: m.cfg.TeamTenant}, who, a, Ticket{})
}

// bodyHTML is a message's text as HTML that is safe to write unescaped: the
// application's arandu-markdown output, which sanitizes it, or the text
// escaped with its line breaks kept.
func (m *Module) bodyHTML(body string) template.HTML {
	if m.cfg.Markdown != nil {
		return m.cfg.Markdown.Render(body).HTML()
	}
	escaped := template.HTMLEscapeString(strings.ReplaceAll(body, "\r\n", "\n"))
	return template.HTML(strings.ReplaceAll(escaped, "\n", "<br>")) //nolint:gosec // escaped on the line above
}

// messageRows are a conversation's messages as the screens draw them.
func (m *Module) messageRows(sc Screen, messages []MessageView, file func(attachment string) string) []MessageRow {
	rows := make([]MessageRow, 0, len(messages))
	for _, msg := range messages {
		author := msg.AuthorName
		if author == "" {
			author = sc.Labels.Role(msg.Role)
		}
		row := MessageRow{
			ID: msg.ID, Author: author, Role: sc.Labels.Role(msg.Role), RoleKey: string(msg.Role),
			When: sc.When(msg.CreatedAt), WhenISO: msg.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Internal: msg.Internal, Body: m.bodyHTML(msg.Body),
		}
		for _, f := range msg.Attachments {
			row.Files = append(row.Files, FileRow{Name: f.Name, Type: f.ContentType, Size: size(f.Size), URL: file(f.ID)})
		}
		rows = append(rows, row)
	}
	return rows
}

func (m *Module) indexPage(ctx *fhttp.Context, sc Screen, who security.Subject, p params, page Page) IndexPageData {
	u := m.URLs()
	rows := make([]TicketRow, 0, len(page.Rows))
	for _, t := range page.Rows {
		rows = append(rows, sc.ticketRow(t, u.Show(t.ID)))
	}
	chosen := make([]string, 0, len(p.statuses))
	for _, s := range p.statuses {
		chosen = append(chosen, string(s))
	}
	return IndexPageData{
		Screen: sc, Rows: rows, Query: p.search, Statuses: chosen, SortKey: p.sort, SortDir: p.dir,
		URL: u.Index(), NewURL: u.New(), CanCreate: m.allows(ctx, who, TicketCreate, Ticket{}),
		Pager:       pager(len(rows), page.Total, m.cfg.PageSize, page.Page, u.Index(), listQueryValues(p)),
		CurrentPage: page.Page, Pages: page.Pages, Total: page.Total, PerPage: m.cfg.PageSize,
		StatusOptions: sc.StatusOptions(),
	}
}

func (m *Module) newPage(sc Screen) NewPageData {
	return NewPageData{
		Screen: sc, Action: m.URLs().Index(), BackURL: m.URLs().Index(), Files: m.cfg.Files != nil,
		MaxFiles: m.cfg.MaxAttachments, MaxMB: megabytes(m.cfg.MaxAttachmentBytes), Accept: accept(),
	}
}

func (m *Module) showPage(ctx *fhttp.Context, sc Screen, who security.Subject, conv Conversation) ShowPageData {
	u := m.URLs()
	t := conv.Ticket
	record := recordOf(t)
	ticket := sc.ticketRow(t, u.Show(t.ID))
	return ShowPageData{
		Screen: sc, Ticket: ticket, BackURL: u.Index(),
		Messages: m.messageRows(sc, conv.Messages, func(id string) string { return u.Attachment(t.ID, id) }),
		ReplyURL: u.Reply(t.ID), CloseURL: u.Close(t.ID), ReopenURL: u.Reopen(t.ID),
		CanReply:  record.CanReply() && m.allows(ctx, who, TicketReply, record),
		CanClose:  record.CanClose() && m.allows(ctx, who, TicketClose, record),
		CanReopen: record.CanReopen() && m.allows(ctx, who, TicketClose, record),
		Files:     m.cfg.Files != nil, MaxFiles: m.cfg.MaxAttachments, MaxMB: megabytes(m.cfg.MaxAttachmentBytes), Accept: accept(),
	}
}

// assigneeChoices are the people a ticket can be given to, the signed-in member
// first, with the one it already has kept in the list so the select never
// silently changes it.
func (sc Screen) assigneeChoices(who security.Subject, people []Person, current Summary) []components.SelectOption {
	var out []components.SelectOption
	seen := map[string]bool{"": true}
	add := func(id, name string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if name == "" {
			name = id
		}
		out = append(out, components.SelectOption{Value: id, Label: name})
	}
	add(who.ID, sc.Labels.T("control.me"))
	for _, person := range people {
		add(person.ID, person.Name)
	}
	add(current.AssigneeID, current.AssigneeName)
	return out
}

func (m *Module) teamIndexPage(sc Screen, who security.Subject, p params, page Page, people []Person) TeamIndexPageData {
	u := m.URLs()
	rows := make([]TicketRow, 0, len(page.Rows))
	for _, t := range page.Rows {
		rows = append(rows, sc.ticketRow(t, u.TeamShow(t.ID)))
	}
	statuses := make([]string, 0, len(p.statuses))
	for _, s := range p.statuses {
		statuses = append(statuses, string(s))
	}
	priorities := make([]string, 0, len(p.priorities))
	for _, v := range p.priorities {
		priorities = append(priorities, string(v))
	}
	options := append(sc.assigneeChoices(who, people, Summary{}), components.SelectOption{Value: AssigneeNone, Label: sc.Labels.T("filter.unassigned")})
	return TeamIndexPageData{
		Screen: sc, Rows: rows, Query: p.search, Statuses: statuses, Priorities: priorities, Assignee: p.assignee,
		SortKey: p.sort, SortDir: p.dir, URL: u.TeamIndex(),
		Pager:       pager(len(rows), page.Total, m.cfg.PageSize, page.Page, u.TeamIndex(), listQueryValues(p)),
		CurrentPage: page.Page, Pages: page.Pages, Total: page.Total,
		StatusOptions: sc.StatusOptions(), PriorityOptions: sc.PriorityOptions(), AssigneeOptions: options,
	}
}

func (m *Module) teamShowPage(ctx *fhttp.Context, sc Screen, who security.Subject, conv Conversation, people []Person) TeamShowPageData {
	u := m.URLs()
	t := conv.Ticket
	record := recordOf(t)
	ticket := sc.ticketRow(t, u.TeamShow(t.ID))
	manage := m.teamAllows(ctx, who, TicketManage)
	return TeamShowPageData{
		Screen: sc, Ticket: ticket, BackURL: u.TeamIndex(),
		Messages: m.messageRows(sc, conv.Messages, func(id string) string { return u.TeamAttachment(t.ID, id) }),
		ReplyURL: u.TeamReply(t.ID), NoteURL: u.TeamNote(t.ID), StatusURL: u.TeamStatus(t.ID),
		PriorityURL: u.TeamPriority(t.ID), AssignURL: u.TeamAssign(t.ID),
		CanReply:      record.CanReply() && manage,
		CanNote:       m.teamAllows(ctx, who, TicketNote),
		CanManage:     manage,
		CanAssign:     m.teamAllows(ctx, who, TicketAssign),
		StatusOptions: sc.StatusOptions(), PriorityOptions: sc.PriorityOptions(),
		AssigneeOptions: append([]components.SelectOption{{Value: "", Label: sc.Labels.T("control.unassigned")}}, sc.assigneeChoices(who, people, t)...),
		Files:           m.cfg.Files != nil, MaxFiles: m.cfg.MaxAttachments, MaxMB: megabytes(m.cfg.MaxAttachmentBytes), Accept: accept(),
	}
}

// listQueryValues are the parameters a list carries onto its page links.
func listQueryValues(p params) url.Values {
	values := url.Values{}
	if p.search != "" {
		values.Set("q", p.search)
	}
	for _, s := range p.statuses {
		values.Add("status", string(s))
	}
	for _, v := range p.priorities {
		values.Add("priority", string(v))
	}
	if p.assignee != "" {
		values.Set("assignee", p.assignee)
	}
	if p.sort != "" {
		values.Set("sort", p.sort)
	}
	if p.dir != "" {
		values.Set("dir", p.dir)
	}
	return values
}
