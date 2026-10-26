package ticket

import (
	"errors"
	"io"
	"mime"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/filesystem"
)

// The handlers are thin on purpose: read the input, ask the service, answer. No
// rule, database handle or Model construction lives here. A handler that
// reached data directly would skip the service's policy boundary, and the
// layout makes that visible rather than relying on review.
//
// Who is asking comes from ctx.User, which reads the subject RequireAuth put on
// the request. No handler loads the session again or takes an identity from the
// input, and none takes a tenant from anywhere: the tenant is the Grant's.
//
// An error from the service is returned as it is, and the router answers it:
// validation.Errors goes back where the request came from with the messages in
// the flash, ErrNotFound is 404 because it wraps model.ErrModelNotFound, and a
// policy refusal is 403. The answer is the status and its standard sentence,
// never the error's own text -- telling a client why a policy said no tells it
// what exists, one request at a time, and the reason belongs in the log. Any
// other error is one nobody expected, and it reaches the error page rather than
// a 200 with an empty body.
//
// Every GET answers the screen to a browser and JSON to a client that asks for
// it; every form posts to the same routes the JSON clients use.

// handlers are the handlers of the route table, by route name.
func (m *Module) handlers() map[string]func(*fhttp.Context) error {
	return map[string]func(*fhttp.Context) error{
		"ticket.index":      m.index,
		"ticket.create":     m.create,
		"ticket.store":      m.store,
		"ticket.show":       m.show,
		"ticket.reply":      m.reply,
		"ticket.close":      m.closeTicket,
		"ticket.reopen":     m.reopen,
		"ticket.attachment": m.attachment,

		"ticket.team.index":      m.teamIndex,
		"ticket.team.show":       m.teamShow,
		"ticket.team.reply":      m.teamReply,
		"ticket.team.note":       m.teamNote,
		"ticket.team.status":     m.teamStatus,
		"ticket.team.priority":   m.teamPriority,
		"ticket.team.assign":     m.teamAssign,
		"ticket.team.attachment": m.teamAttachment,
	}
}

// index answers the customer's tickets.
func (m *Module) index(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	params := listParams(ctx, sortableTicket)
	page, err := m.tickets.List(ctx.Ctx(), who, ListQuery{
		Search: params.search, Statuses: params.statuses, Sort: params.sort, Dir: params.dir, Page: params.page, PerPage: m.cfg.PageSize,
	})
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, pageResource(page, customerFields))
	}
	sc, err := m.screen(ctx, who, "screen.index_title", false)
	if err != nil {
		return err
	}
	return ctx.View(ViewIndex, m.indexPage(ctx, sc, who, params, page))
}

// create answers the form that opens a ticket.
func (m *Module) create(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	if err := m.tickets.CanOpen(ctx.Ctx(), who); err != nil {
		return err
	}
	sc, err := m.screen(ctx, who, "screen.new_title", false)
	if err != nil {
		return err
	}
	return ctx.View(ViewNew, m.newPage(sc))
}

// store opens a ticket.
func (m *Module) store(ctx *fhttp.Context) error {
	values, uploads, done, err := m.readForm(ctx)
	if err != nil {
		return err
	}
	defer done()

	who, _ := ctx.User()
	record, err := m.tickets.Open(ctx.Ctx(), who, OpenRequest{
		Subject: values.Get("subject"), Category: values.Get("category"), Body: values.Get("body"),
	}, uploads)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, fieldsResource(customerFields(summaryOf(record))))
	}
	return ctx.Redirect(m.URLs().Show(record.ID))
}

// show answers one conversation.
func (m *Module) show(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	conv, err := m.tickets.Find(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, customerConversation(conv))
	}
	sc, err := m.screen(ctx, who, "screen.show_title", false)
	if err != nil {
		return err
	}
	return ctx.View(ViewShow, m.showPage(ctx, sc, who, conv))
}

// reply adds the customer's message.
func (m *Module) reply(ctx *fhttp.Context) error {
	values, uploads, done, err := m.readForm(ctx)
	if err != nil {
		return err
	}
	defer done()

	who, _ := ctx.User()
	id := ctx.Param("id")
	sent, err := m.tickets.Reply(ctx.Ctx(), who, id, ReplyRequest{Body: values.Get("body")}, uploads)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, fieldsResource(messageFields(messageViewOf(sent, nil))))
	}
	return ctx.Redirect(m.URLs().Show(id))
}

// closeTicket closes a ticket.
func (m *Module) closeTicket(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	record, err := m.tickets.Close(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, fieldsResource(customerFields(summaryOf(record))))
	}
	return ctx.Redirect(m.URLs().Show(record.ID))
}

// reopen brings a ticket back to the team.
func (m *Module) reopen(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	record, err := m.tickets.Reopen(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, fieldsResource(customerFields(summaryOf(record))))
	}
	return ctx.Redirect(m.URLs().Show(record.ID))
}

// attachment streams a file of a message to the customer, as a download.
func (m *Module) attachment(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	file, body, err := m.tickets.Attachment(ctx.Ctx(), who, ctx.Param("id"), ctx.Param("attachment"))
	if err != nil {
		return err
	}
	return serve(ctx, file, body)
}

// teamIndex answers the tickets of every space.
func (m *Module) teamIndex(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	params := listParams(ctx, sortableTeam)
	if params.assignee == "me" {
		params.assignee = who.ID
	}
	page, err := m.team.List(ctx.Ctx(), who, TeamQuery{
		Search: params.search, Statuses: params.statuses, Priorities: params.priorities, Assignee: params.assignee,
		Tenant: ctx.Query("space"), Sort: params.sort, Dir: params.dir, Page: params.page, PerPage: m.cfg.PageSize,
	})
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, pageResource(page, teamFields))
	}
	sc, err := m.screen(ctx, who, "screen.team_index_title", true)
	if err != nil {
		return err
	}
	people, err := m.team.Team(ctx.Ctx(), who)
	if err != nil {
		return err
	}
	return ctx.View(ViewTeamIndex, m.teamIndexPage(sc, who, params, page, people))
}

// teamShow answers one conversation, with its internal notes.
func (m *Module) teamShow(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	conv, err := m.team.Find(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, teamConversation(conv))
	}
	sc, err := m.screen(ctx, who, "screen.team_show_title", true)
	if err != nil {
		return err
	}
	people, err := m.team.Team(ctx.Ctx(), who)
	if err != nil {
		return err
	}
	return ctx.View(ViewTeamShow, m.teamShowPage(ctx, sc, who, conv, people))
}

// teamReply answers the customer.
func (m *Module) teamReply(ctx *fhttp.Context) error {
	values, uploads, done, err := m.readForm(ctx)
	if err != nil {
		return err
	}
	defer done()

	who, _ := ctx.User()
	id := ctx.Param("id")
	sent, err := m.team.Reply(ctx.Ctx(), who, id, ReplyRequest{Body: values.Get("body")}, uploads)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, fieldsResource(messageFields(messageViewOf(sent, nil))))
	}
	return ctx.Redirect(m.URLs().TeamShow(id))
}

// teamNote writes a note only the team reads.
func (m *Module) teamNote(ctx *fhttp.Context) error {
	values, uploads, done, err := m.readForm(ctx)
	if err != nil {
		return err
	}
	defer done()

	who, _ := ctx.User()
	id := ctx.Param("id")
	note, err := m.team.Note(ctx.Ctx(), who, id, ReplyRequest{Body: values.Get("body")}, uploads)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, fieldsResource(messageFields(messageViewOf(note, nil))))
	}
	return ctx.Redirect(m.URLs().TeamShow(id))
}

// teamStatus moves a ticket to the status the team chose.
func (m *Module) teamStatus(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	record, err := m.team.SetStatus(ctx.Ctx(), who, ctx.Param("id"), Status(ctx.Input("status")))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, fieldsResource(teamFields(summaryOf(record))))
	}
	return ctx.Redirect(m.URLs().TeamShow(record.ID))
}

// teamPriority changes how soon the team looks at a ticket.
func (m *Module) teamPriority(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	record, err := m.team.SetPriority(ctx.Ctx(), who, ctx.Param("id"), Priority(ctx.Input("priority")))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, fieldsResource(teamFields(summaryOf(record))))
	}
	return ctx.Redirect(m.URLs().TeamShow(record.ID))
}

// teamAssign gives a ticket to a member of the team, or takes it back.
func (m *Module) teamAssign(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	record, err := m.team.Assign(ctx.Ctx(), who, ctx.Param("id"), ctx.Input("assignee"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, fieldsResource(teamFields(summaryOf(record))))
	}
	return ctx.Redirect(m.URLs().TeamShow(record.ID))
}

// teamAttachment streams a file of a message to the team, as a download.
func (m *Module) teamAttachment(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	file, body, err := m.team.Attachment(ctx.Ctx(), who, ctx.Param("id"), ctx.Param("attachment"))
	if err != nil {
		return err
	}
	return serve(ctx, file, body)
}

// serve writes a file as a download: the type the package detected and not one
// the sender announced, never inline, never cached, never sniffed, and under a
// policy that lets nothing in it run.
func serve(ctx *fhttp.Context, file *Attachment, body filesystem.File) error {
	defer body.Body.Close()
	header := ctx.Response.Header()
	header.Set("Content-Type", file.ContentType)
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	header.Set("Content-Length", strconv.FormatInt(file.Size, 10))
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.Response.WriteHeader(stdhttp.StatusOK)
	_, err := io.Copy(ctx.Response, body.Body)
	return err
}

// params are what a list's address says, read and cleaned: a status that is not
// one, and a sort the list does not offer, are dropped rather than refused,
// because an address somebody edited by hand is not a form to send back.
type params struct {
	search     string
	statuses   []Status
	priorities []Priority
	assignee   string
	sort, dir  string
	page       int
}

// listParams reads the query string of a list. The sort is kept only when the
// allowlist has it.
func listParams(ctx *fhttp.Context, allowed map[string]string) params {
	query := ctx.Request.URL.Query()
	p := params{search: strings.TrimSpace(query.Get("q")), assignee: strings.TrimSpace(query.Get("assignee"))}
	for _, raw := range query["status"] {
		if s := Status(strings.TrimSpace(raw)); s.Valid() {
			p.statuses = append(p.statuses, s)
		}
	}
	for _, raw := range query["priority"] {
		if v := Priority(strings.TrimSpace(raw)); v.Valid() {
			p.priorities = append(p.priorities, v)
		}
	}
	if sort := query.Get("sort"); sort != "" {
		if _, ok := allowed[sort]; ok {
			p.sort = sort
		}
	}
	if query.Get("dir") == "asc" {
		p.dir = "asc"
	}
	p.page, _ = strconv.Atoi(query.Get("page"))
	return p
}

// pageResource is a page of a list as one response.
func pageResource(page Page, fields func(Summary) map[string]any) fieldsResource {
	items := make([]map[string]any, 0, len(page.Rows))
	for _, row := range page.Rows {
		items = append(items, fields(row))
	}
	return fieldsResource{"items": items, "total": page.Total, "page": page.Page, "pages": page.Pages}
}

// readForm reads a form that carries a message: its text fields and the files
// that came with it. The body is bounded before it is read, so a request cannot
// make the server hold more than the files are allowed to weigh; whoever calls
// it closes what it opened with the function it returns.
func (m *Module) readForm(ctx *fhttp.Context) (url.Values, []Upload, func(), error) {
	limit := m.cfg.MaxAttachmentBytes*int64(m.cfg.MaxAttachments) + (1 << 20)
	ctx.Request.Body = stdhttp.MaxBytesReader(ctx.Response, ctx.Request.Body, limit)
	if err := ctx.Request.ParseMultipartForm(8 << 20); err != nil && !errors.Is(err, stdhttp.ErrNotMultipart) {
		return nil, nil, nil, validation.Errors{"files": {m.Labels().message("multipart")}}
	}
	var (
		uploads []Upload
		opened  []io.Closer
	)
	done := func() {
		for _, c := range opened {
			_ = c.Close()
		}
		if ctx.Request.MultipartForm != nil {
			_ = ctx.Request.MultipartForm.RemoveAll()
		}
	}
	if form := ctx.Request.MultipartForm; form != nil {
		for _, header := range form.File["files"] {
			// A browser sends an empty part for a file input nobody filled.
			if header.Filename == "" && header.Size == 0 {
				continue
			}
			if len(uploads) > m.cfg.MaxAttachments {
				break
			}
			file, err := header.Open()
			if err != nil {
				done()
				return nil, nil, nil, err
			}
			opened = append(opened, file)
			uploads = append(uploads, Upload{Name: header.Filename, Body: file})
		}
	}
	return ctx.Request.PostForm, uploads, done, nil
}
