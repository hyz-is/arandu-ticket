package ticket

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	fview "github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/pagination"
	hview "github.com/arandu-io/hesape/view"
	"github.com/arandu-io/kyse/components"
)

// What the screens show, built from what the services answer. Nothing here
// reads the database or asks the policy: the handler already did both, and
// these only turn statuses, instants and bodies into what a page draws.

// The screens this package renders, by the name their path gives them.
const (
	ViewIndex     = "modules.ticket.index"
	ViewNew       = "modules.ticket.form"
	ViewShow      = "modules.ticket.show"
	ViewTeamIndex = "modules.ticket.admin.index"
	ViewTeamShow  = "modules.ticket.admin.show"
)

// URLs are the addresses of the routes under the module's configuration. A link
// is built from here and never written out a second time: under another
// Prefix the paths move and the names do not.
type URLs struct{ cfg Config }

func (u URLs) show(base, id string) string { return base + "/" + url.PathEscape(id) }

// Index is the customer's list.
func (u URLs) Index() string { return u.cfg.Prefix }

// New is the form that opens a ticket.
func (u URLs) New() string { return u.cfg.Prefix + u.cfg.Paths.New }

// Show is one conversation, for the customer.
func (u URLs) Show(id string) string { return u.show(u.cfg.Prefix, id) }

// Reply is where the customer posts a reply.
func (u URLs) Reply(id string) string { return u.Show(id) + u.cfg.Paths.Replies }

// Close is where the customer posts a closing.
func (u URLs) Close(id string) string { return u.Show(id) + u.cfg.Paths.Closure }

// Reopen is where the customer posts a reopening.
func (u URLs) Reopen(id string) string { return u.Show(id) + u.cfg.Paths.Reopening }

// Attachment is a file of a message, for the customer.
func (u URLs) Attachment(id, attachment string) string {
	return u.Show(id) + u.cfg.Paths.Attachments + "/" + url.PathEscape(attachment)
}

// TeamIndex is the team's list.
func (u URLs) TeamIndex() string { return u.cfg.TeamPrefix }

// TeamShow is one conversation, for the team.
func (u URLs) TeamShow(id string) string { return u.show(u.cfg.TeamPrefix, id) }

// TeamReply is where the team posts an answer.
func (u URLs) TeamReply(id string) string { return u.TeamShow(id) + u.cfg.Paths.Replies }

// TeamNote is where the team posts an internal note.
func (u URLs) TeamNote(id string) string { return u.TeamShow(id) + u.cfg.Paths.Notes }

// TeamStatus is where the team posts a new status.
func (u URLs) TeamStatus(id string) string { return u.TeamShow(id) + u.cfg.Paths.Status }

// TeamPriority is where the team posts a new priority.
func (u URLs) TeamPriority(id string) string { return u.TeamShow(id) + u.cfg.Paths.Priority }

// TeamAssign is where the team posts a new assignee.
func (u URLs) TeamAssign(id string) string { return u.TeamShow(id) + u.cfg.Paths.Assignee }

// TeamAttachment is a file of a message, for the team.
func (u URLs) TeamAttachment(id, attachment string) string {
	return u.TeamShow(id) + u.cfg.Paths.Attachments + "/" + url.PathEscape(attachment)
}

// Screen is what every page of this package carries: the page hesape's view.New
// builds, the application's chrome, and the sentences.
//
// It embeds hesape's view.Page, so every page of this package is a
// hesape/view.Layout -- what the layouts of an application on Arandu framework
// v0.62 and newer assert -- and it answers HasErrors and ErrorSummary as well,
// so it is also the framework/view.Layout an older layout asks for.
type Screen struct {
	hview.Page
	Labels Labels
	// URLs are the addresses of the package's routes.
	URLs URLs
	// Location is the timezone instants are written in.
	Location *time.Location

	chrome any
}

// Compile-time proof that a page of this package fits both layout contracts.
var (
	_ hview.Layout   = Screen{}
	_ fview.Layout   = Screen{}
	_ slog.LogValuer = Screen{}
)

// Chrome is what Config.Chrome answered for this screen: the application's
// sidebar, navigation and account menu, in whatever type its layout draws from.
// A layout asserts that type on it; nil when Config.Chrome is nil.
func (s Screen) Chrome() any { return s.chrome }

// HasErrors says whether the attempt that landed here was rejected:
// framework/view.Layout's name for hesape's Any.
func (s Screen) HasErrors() bool { return s.Page.Any() }

// All is one line per rejected message, for the banner above a form, in the
// screen's language: the field is named by its label in the catalogue
// ("Assunto: Preencha este campo.").
func (s Screen) All() []string { return s.Labels.Summary(s.Page.Errors) }

// ErrorSummary is one line per rejected field: framework/view.Layout's name for
// All.
func (s Screen) ErrorSummary() []string { return s.All() }

// FieldErrors is every message for one field.
func (s Screen) FieldErrors(name string) []string { return s.Page.Get(name) }

// MarshalJSON writes the page with the CSRF token redacted, as
// framework/view.Page does: a page dumped for debugging never publishes the
// token. It answers for every page data that embeds Screen.
func (s Screen) MarshalJSON() ([]byte, error) { return framePage(s.Page).MarshalJSON() }

// LogValue is the page as a log line carries it, the token redacted.
func (s Screen) LogValue() slog.Value { return framePage(s.Page).LogValue() }

// Form is the form state of the page.
func (s Screen) Form() FormState { return FormState{Page: s.Page} }

// FormState is what a form field reads from the page it is drawn on: what was
// typed before a refusal, and the message for each field.
type FormState struct{ hview.Page }

// FieldError is the first message for one field, empty when it was accepted.
func (f FormState) FieldError(name string) string { return f.Page.FieldError(name) }

// HasErrors says whether the attempt that landed here was rejected.
func (f FormState) HasErrors() bool { return f.Page.Any() }

// ErrorSummary is one line per rejected field.
func (f FormState) ErrorSummary() []string { return f.Page.All() }

// FieldErrors is every message for one field.
func (f FormState) FieldErrors(name string) []string { return f.Page.Get(name) }

// MarshalJSON writes the form state with the CSRF token redacted.
func (f FormState) MarshalJSON() ([]byte, error) { return framePage(f.Page).MarshalJSON() }

// LogValue is the form state as a log line carries it, the token redacted.
func (f FormState) LogValue() slog.Value { return framePage(f.Page).LogValue() }

// framePage copies a hesape page into framework/view.Page, by name, for the two
// methods that redact the token there.
func framePage(p hview.Page) fview.Page {
	return fview.Page{
		Title: p.Title, Description: p.Description, Canonical: p.Canonical, AppName: p.AppName, Token: p.Token,
		Authenticated: p.Authenticated, UserName: p.UserName,
		HomeURL: p.HomeURL, LoginURL: p.LoginURL, LogoutURL: p.LogoutURL, RegisterURL: p.RegisterURL,
		PanelURL: p.PanelURL, AdminURL: p.AdminURL,
		Impersonating: p.Impersonating, ImpersonationActs: p.ImpersonationActs, Impersonator: p.Impersonator,
		LeaveImpersonationURL: p.LeaveImpersonationURL,
		Path:                  p.Path, Errors: p.Errors, Old: p.Old, Messages: p.Messages,
	}
}

// TableLines are the sentences a data table on these screens says.
func (s Screen) TableLines() components.DataTableLines {
	l := s.Labels
	return components.DataTableLines{
		Showing: l.T("table.showing"), PerPage: l.T("table.per_page"), NoResult: l.T("table.no_result"),
		Previous: l.T("table.previous"), Next: l.T("table.next"), Page: l.T("table.page"), PageOf: l.T("table.page_of"),
		Back: l.T("table.back"), Forward: l.T("table.forward"), SelectAll: l.T("table.select_all"), Bulk: l.T("table.bulk"),
		Apply: l.T("table.apply"), Ascending: l.T("table.ascending"), Descending: l.T("table.descending"), Unsorted: l.T("table.unsorted"),
	}
}

// SearchLabel is the name a screen reader gives a table's search box:
// "Buscar em Chamados".
func (s Screen) SearchLabel(table string) string {
	return s.Labels.line("control.search_label", map[string]any{"label": table})
}

// StatusOptions are the statuses as options of a filter or a select.
func (s Screen) StatusOptions() []components.SelectOption {
	out := make([]components.SelectOption, 0, len(Statuses))
	for _, status := range Statuses {
		out = append(out, components.SelectOption{Value: string(status), Label: s.Labels.Status(status)})
	}
	return out
}

// PriorityOptions are the priorities as options of a filter or a select.
func (s Screen) PriorityOptions() []components.SelectOption {
	out := make([]components.SelectOption, 0, len(Priorities))
	for _, priority := range Priorities {
		out = append(out, components.SelectOption{Value: string(priority), Label: s.Labels.Priority(priority)})
	}
	return out
}

// When is an instant as Brazilians write it, in the screen's timezone.
func (s Screen) When(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	loc := s.Location
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02/01/2006 15:04")
}

// TicketRow is one ticket as a list or a conversation shows it.
type TicketRow struct {
	ID, Code, Subject, Category string
	Status, StatusKey           string
	Priority, PriorityKey       string
	Requester, RequesterEmail   string
	Assignee, AssigneeID        string
	Space, Origin               string
	Created, Updated            string
	// CreatedSort and UpdatedSort order like the instants they stand for.
	CreatedSort, UpdatedSort string
	URL                      string
	Closed, Resolved         bool
}

// ticketRow is a ticket as a screen shows it.
func (s Screen) ticketRow(t Summary, open string) TicketRow {
	requester := t.RequesterName
	if requester == "" {
		requester = t.RequesterEmail
	}
	if requester == "" {
		requester = t.RequesterID
	}
	return TicketRow{
		ID: t.ID, Code: t.Code(), Subject: t.Subject, Category: t.Category,
		Status: s.Labels.Status(t.Status), StatusKey: string(t.Status),
		Priority: s.Labels.Priority(t.Priority), PriorityKey: string(t.Priority),
		Requester: requester, RequesterEmail: t.RequesterEmail,
		Assignee: t.AssigneeName, AssigneeID: t.AssigneeID,
		Space: t.Tenant, Origin: s.Labels.Origin(t.Origin),
		Created: s.When(t.CreatedAt), Updated: s.When(t.UpdatedAt),
		CreatedSort: t.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedSort: t.UpdatedAt.UTC().Format(time.RFC3339Nano),
		URL:    open,
		Closed: t.Status == StatusClosed, Resolved: t.Status == StatusResolved,
	}
}

// FileRow is one file of a message.
type FileRow struct{ Name, Type, Size, URL string }

// MessageRow is one message of a conversation.
type MessageRow struct {
	ID, Author, Role, RoleKey string
	When, WhenISO             string
	// Internal marks a note only the team reads; the customer's rows hold none.
	Internal bool
	// Body is the text as sanitized HTML: Config.Markdown's output, or the text
	// escaped. It is the one value on a page written without escaping, and
	// nothing reaches it that did not pass through one of those two.
	Body  template.HTML
	Files []FileRow
}

// IndexPageData is the customer's list, a page at a time, filtered, searched
// and ordered on the server.
type IndexPageData struct {
	Screen
	Rows          []TicketRow
	Query         string
	Statuses      []string
	SortKey       string
	SortDir       string
	URL, NewURL   string
	CanCreate     bool
	Pager         components.Paginator
	CurrentPage   int
	Pages, Total  int
	PerPage       int
	StatusOptions []components.SelectOption
}

// NewPageData is the form that opens a ticket.
type NewPageData struct {
	Screen
	Action, BackURL string
	// Files says the form takes attachments, and the rest says which.
	Files    bool
	MaxFiles int
	MaxMB    string
	Accept   string
}

// ShowPageData is one conversation, for the customer.
type ShowPageData struct {
	Screen
	Ticket                        TicketRow
	Messages                      []MessageRow
	BackURL                       string
	ReplyURL, CloseURL, ReopenURL string
	CanReply, CanClose            bool
	CanReopen                     bool
	Files                         bool
	MaxFiles                      int
	MaxMB                         string
	Accept                        string
}

// TeamIndexPageData is the team's list, a page at a time, filtered, searched
// and ordered on the server.
type TeamIndexPageData struct {
	Screen
	Rows            []TicketRow
	Query           string
	Statuses        []string
	Priorities      []string
	Assignee        string
	Space           string
	SortKey         string
	SortDir         string
	URL             string
	Pager           components.Paginator
	CurrentPage     int
	Pages, Total    int
	StatusOptions   []components.SelectOption
	PriorityOptions []components.SelectOption
	AssigneeOptions []components.SelectOption
}

// TeamShowPageData is one conversation, for the team, with its internal notes
// and the controls that change the ticket.
type TeamShowPageData struct {
	Screen
	Ticket          TicketRow
	Messages        []MessageRow
	BackURL         string
	ReplyURL        string
	NoteURL         string
	StatusURL       string
	PriorityURL     string
	AssignURL       string
	CanReply        bool
	CanNote         bool
	CanManage       bool
	CanAssign       bool
	StatusOptions   []components.SelectOption
	PriorityOptions []components.SelectOption
	AssigneeOptions []components.SelectOption
	Files           bool
	MaxFiles        int
	MaxMB           string
	Accept          string
}

// pager is the page of a list the data table reads its position from.
func pager(rows, total, perPage, page int, path string, query url.Values) components.Paginator {
	return pagination.NewLengthAwarePage(rows, total, perPage, page, pagination.Options{Path: path, Query: query})
}

// accept is the value of the file input's accept attribute: the types a message
// may carry.
func accept() string { return strings.Join(AttachmentTypes(), ",") }

// size is a file's size as people read it.
func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	}
	return strconv.FormatInt(n, 10) + " B"
}

// StatusVariant is the badge variant a status wears: the one that waits on the
// customer stands out, the ones that are over fade.
func StatusVariant(key string) string {
	switch Status(key) {
	case StatusWaitingCustomer:
		return "destructive"
	case StatusResolved, StatusClosed:
		return "outline"
	case StatusOpen, StatusWaitingTeam:
		return "secondary"
	}
	return ""
}

// PriorityVariant is the badge variant a priority wears: the ordinary ones are
// quiet, the ones that ask for haste are not.
func PriorityVariant(key string) string {
	switch Priority(key) {
	case PriorityUrgent:
		return "destructive"
	case PriorityHigh:
		return ""
	case PriorityLow:
		return "outline"
	}
	return "secondary"
}

// FilesHint is the sentence under a file input: what may be sent, and how much.
func (s Screen) FilesHint(max int, mb string) string {
	return s.Labels.line("screen.files_hint", map[string]any{"max": max, "mb": mb})
}

// NewURLs are the addresses of the routes under a pair of prefixes and the
// segments, for a screen built outside a request: a test that draws a page, an
// e-mail that links to one. Empty values are the defaults.
func NewURLs(prefix, teamPrefix string, paths Paths) URLs {
	return URLs{cfg: Config{Prefix: prefix, TeamPrefix: teamPrefix, Paths: paths}.withDefaults()}
}

// Prose is a message's text as markup a page may write unescaped. It is a
// component in the sense the view compiler's audit means: a function returning
// template.HTML whose input was made safe when the row was built -- escaped, or
// rendered and sanitized by arandu-markdown -- and so is the one door through
// which text a person typed reaches a page as markup.
func Prose(message MessageRow) template.HTML { return message.Body }
