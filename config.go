package ticket

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/translation"
	activitylog "github.com/hyz-is/arandu-activitylog"
	markdown "github.com/hyz-is/arandu-markdown"
)

// The defaults for the optional settings. They are constants rather than
// literals inside Config.withDefaults, so the value a reader finds here is the
// value the package uses.
const (
	// DefaultPrefix is where the customer's routes are mounted when Config
	// leaves Prefix empty.
	DefaultPrefix = "/tickets"
	// DefaultTeamPrefix is where the team's routes are mounted when Config
	// leaves TeamPrefix empty.
	DefaultTeamPrefix = "/admin/tickets"
	// DefaultPageSize is how many rows one page of a list answers with when
	// Config leaves PageSize at zero.
	DefaultPageSize = 25
	// MaxPageSize is the ceiling PageSize is refused above. A page nobody
	// bounded is a page that reads the whole table on the day the table is
	// large.
	MaxPageSize = 100
	// DefaultLocale is the language of the screens, the messages and the
	// activity descriptions when Config.Locale names none.
	DefaultLocale = "pt-BR"
	// FallbackLocale answers every line the locale in use does not have.
	FallbackLocale = "en"
	// DefaultContactWindow is how long the same e-mail, subject and body open
	// one ticket and not two.
	DefaultContactWindow = 10 * time.Minute
	// MaxContactWindow is the ceiling ContactWindow is refused above: past a
	// day the same words are a new request.
	MaxContactWindow = 24 * time.Hour
	// DefaultMaxAttachmentBytes is the size of the largest file one message
	// carries when Config leaves MaxAttachmentBytes at zero.
	DefaultMaxAttachmentBytes = 5 << 20
	// MaxAttachmentBytesLimit is the ceiling MaxAttachmentBytes is refused
	// above: the file is read into memory to be looked at before it is kept.
	MaxAttachmentBytesLimit = 25 << 20
	// DefaultMaxAttachments is how many files one message carries when Config
	// leaves MaxAttachments at zero.
	DefaultMaxAttachments = 5
	// MaxAttachmentsLimit is the ceiling MaxAttachments is refused above.
	MaxAttachmentsLimit = 10
	// MaxBodyChars is the longest message body, in characters. Markdown is
	// rendered in a pass as long as the text, so a field that takes text from
	// people the application does not trust caps its length.
	MaxBodyChars = 10000
)

// Config is what the application passes when it wires this package.
//
// A typed struct rather than a map: a misspelled key in a map is a setting that
// silently keeps its default, and the failure shows up as behaviour nobody
// asked for rather than as an error. Here a field that does not exist does not
// compile.
//
// It names no tenant for the customers, and that is deliberate. Every route
// sits behind RequireAuth, so the tenant of every customer statement is the one
// the Grant carries, from the session of whoever is asking; a tenant written
// here would be a second place for one to come from. The one tenant it does
// name is the team's own, TeamTenant, because the team is not a customer.
type Config struct {
	// TeamTenant is the tenant the platform's own team signs in to: the realm of
	// the operator console. A team member is a subject of this tenant who holds
	// the team's actions, and the team policy refuses any other tenant's
	// subject whatever actions they carry. It comes from the application's own
	// configuration, never from a request. Required.
	TeamTenant string

	// Prefix is the path the customer's routes are mounted under. Empty means
	// DefaultPrefix.
	Prefix string

	// TeamPrefix is the path the team's routes are mounted under. Empty means
	// DefaultTeamPrefix. The two sides are separate: the team's session is
	// valid on this side only, and the customer's on the other.
	TeamPrefix string

	// Paths are the segments every route is built from, under Prefix and
	// TeamPrefix. A field left empty keeps its DefaultPaths value;
	// PortuguesePaths is the whole set in Brazilian Portuguese.
	Paths Paths

	// PageSize is how many rows one page of a list answers with. Zero means
	// DefaultPageSize, and anything above MaxPageSize is refused rather than
	// clamped: a number somebody wrote and did not get is worse than a number
	// somebody wrote and was told about.
	PageSize int

	// Middleware runs on every route of this package after RequireAuth, in
	// order, and TeamMiddleware after it on the team's routes only. An
	// application whose permissions arandu-permission administers mounts its
	// Roles() in Middleware: RequireAuth puts the subject as the session
	// resolved it, without the actions arandu-permission resolves on each
	// request. A guard of the application's own side -- the operator console's
	// -- goes in TeamMiddleware. A guard answers "is this request on the right
	// side" and stops; the policy still decides.
	Middleware     []func(http.Handler) http.Handler
	TeamMiddleware []func(http.Handler) http.Handler

	// Accounts resolves an e-mail to an account, for OpenFromContact. Nil makes
	// OpenFromContact refuse: there is no account to open for.
	Accounts AccountResolver

	// Directory names people and lists the team. Optional; see Directory.
	Directory Directory

	// Notifier is told when a ticket is opened, when the customer replies and
	// when the team answers. Nil tells nobody. See Notifier.
	Notifier Notifier

	// Activity is arandu-activitylog's logger, the application's one instance
	// (activityModule.Logger()). Every act that changes something is recorded
	// under the Grant it was authorized with, in the log named ActivityLogName,
	// and so is every download of a file. Nil records nothing.
	Activity *activitylog.Logger

	// Markdown renders the text of a message as sanitized HTML. It is the
	// application's one arandu-markdown instance (markdownModule): configure it
	// with Images set to markdown.ImagesNone and LinkRel "noopener nofollow
	// ugc", as a body people wrote deserves. Nil shows the text escaped, with
	// its line breaks, which is safe and plain.
	Markdown *markdown.Module

	// Files is the Disk the attachments are kept on: the application's, which
	// in production is a private bucket. Every key is generated by the server
	// and every read is authorized and recorded; nothing is served by a public
	// address. Nil turns attachments off, and a message that carries a file is
	// refused.
	Files *filesystem.Disk

	// MaxAttachmentBytes is the size of the largest file one message carries.
	// Zero means DefaultMaxAttachmentBytes, and anything above
	// MaxAttachmentBytesLimit is refused.
	MaxAttachmentBytes int64

	// MaxAttachments is how many files one message carries. Zero means
	// DefaultMaxAttachments, and anything above MaxAttachmentsLimit is refused.
	MaxAttachments int

	// ContactWindow is how long the same e-mail, subject and body open one
	// ticket and not two. Zero means DefaultContactWindow.
	ContactWindow time.Duration

	// Translator is the application's own catalogue, asked before the one this
	// package ships, key by key: "ticket.screen.index_title". Optional.
	Translator *translation.Translator

	// Locale is the language every screen, validation message and activity
	// description of this package is written in. Empty means DefaultLocale. The
	// package ships the locales Locales() lists; any other reads the
	// application's Translator first and English after it.
	Locale string

	// TitleSeparator joins a screen's title to the application's name in the
	// document title: with " — ", the list is titled "Chamados — Alpr.sh". Empty
	// titles a screen with its own title alone.
	TitleSeparator string

	// Chrome is what the application's layout draws around a screen of this
	// package: its sidebar, its navigation, the account menu. It is called once
	// per screen, after RequireAuth and before the page is rendered, and what it
	// answers is carried on the page as Screen.Chrome, where a layout asserts
	// the interface it draws from. An error is returned as the handler's. Nil
	// draws the screens with the page hesape's view.New builds and nothing
	// more.
	Chrome func(ctx *fhttp.Context, req ChromeRequest) (any, error)

	// Location is the timezone an instant is written in on the screens: a ticket
	// written at 22:00 in Sao Paulo is shown at 22:00, though it is already the next
	// day in UTC. Nil means DefaultLocation.
	Location *time.Location

	// Now is the clock: every timestamp a row carries is read from it. Nil
	// means time.Now. A test holds it still; an application leaves it nil.
	Now func() time.Time

	// Logger receives what the package cannot return to anybody: a Notifier
	// that failed after the change was stored. Nil means slog.Default().
	Logger *slog.Logger
}

// ChromeRequest is what Config.Chrome is asked about one screen.
type ChromeRequest struct {
	// Title is the screen's title, already in the screen's language, without
	// the application's name Config.TitleSeparator adds to the document title.
	Title string
	// Team says the screen is one of the team's, mounted under TeamPrefix.
	Team bool
	// Subject is who is asking, as RequireAuth and Config.Middleware left it.
	Subject security.Subject
}

// Paths are the segments the routes of this package are built from. Each one
// starts with "/" and holds no wildcard.
//
//	GET  Prefix                                       the customer's tickets
//	GET  Prefix + New                                 the form that opens one
//	POST Prefix                                       open one
//	GET  Prefix + /{id}                               one conversation
//	POST Prefix + /{id} + Replies                     reply
//	POST Prefix + /{id} + Closure                     close
//	POST Prefix + /{id} + Reopening                   reopen
//	GET  Prefix + /{id} + Attachments + /{attachment} a file
//
// and under TeamPrefix the list, one conversation, Replies, Notes, Status,
// Priority, Assignee and Attachments.
type Paths struct {
	New, Replies, Closure, Reopening, Attachments string
	Notes, Status, Priority, Assignee             string
}

// DefaultPaths are the English segments.
var DefaultPaths = Paths{
	New: "/new", Replies: "/replies", Closure: "/closure", Reopening: "/reopening", Attachments: "/attachments",
	Notes: "/notes", Status: "/status", Priority: "/priority", Assignee: "/assignee",
}

// PortuguesePaths are the segments in Brazilian Portuguese, for an application
// whose addresses are written in it. Mounted at "/painel/chamados" and
// "/admin/chamados" they read /painel/chamados/novo, /painel/chamados/{id}/respostas
// and /admin/chamados/{id}/responsavel.
var PortuguesePaths = Paths{
	New: "/novo", Replies: "/respostas", Closure: "/encerramento", Reopening: "/reabertura", Attachments: "/anexos",
	Notes: "/notas", Status: "/situacao", Priority: "/prioridade", Assignee: "/responsavel",
}

// fields are the segments with their names, for validating and filling them.
func (p *Paths) fields() []struct {
	name  string
	value *string
} {
	return []struct {
		name  string
		value *string
	}{
		{"New", &p.New}, {"Replies", &p.Replies}, {"Closure", &p.Closure}, {"Reopening", &p.Reopening},
		{"Attachments", &p.Attachments}, {"Notes", &p.Notes}, {"Status", &p.Status}, {"Priority", &p.Priority},
		{"Assignee", &p.Assignee},
	}
}

// validate reports a segment that cannot be part of a route.
func (p Paths) validate() error {
	for _, f := range p.fields() {
		v := *f.value
		if v == "" {
			continue
		}
		if v[0] != '/' || strings.ContainsAny(v, "{} \t?#") || v == "/" || strings.HasSuffix(v, "/") {
			return fmt.Errorf("ticket: Config.Paths.%s is %q: a segment starts with /, ends without one and holds no wildcard", f.name, v)
		}
	}
	return nil
}

// withDefaults fills every empty segment from DefaultPaths.
func (p Paths) withDefaults() Paths {
	d := DefaultPaths
	defaults := d.fields()
	for i, f := range p.fields() {
		if *f.value == "" {
			*f.value = *defaults[i].value
		}
	}
	return p
}

// DefaultLocation is the timezone the screens write instants in when Config
// leaves Location nil. The package is Brazilian first; an application elsewhere
// sets its own.
const DefaultLocation = "America/Sao_Paulo"

// reservedPrefix is the namespace the framework keeps for itself.
const reservedPrefix = "/_arandu"

// Validate reports what the configuration cannot be used with.
//
// It is called by New, so an application with a setting that cannot work fails
// where it is wired rather than on the first request that needed it.
func (c Config) Validate() error {
	if !security.ValidTenant(c.TeamTenant) {
		return fmt.Errorf("ticket: Config.TeamTenant is %q, which cannot be a tenant: pass the tenant the platform team signs in to (lowercase letters, digits, - and _, up to 64 characters)", c.TeamTenant)
	}
	for _, prefix := range []struct{ name, value string }{{"Prefix", c.Prefix}, {"TeamPrefix", c.TeamPrefix}} {
		if prefix.value == "" {
			continue
		}
		if prefix.value[0] != '/' || (len(prefix.value) > 1 && strings.HasSuffix(prefix.value, "/")) {
			return fmt.Errorf("ticket: Config.%s is %q and has to start with / and end without one", prefix.name, prefix.value)
		}
		if prefix.value == reservedPrefix || strings.HasPrefix(prefix.value, reservedPrefix+"/") {
			return fmt.Errorf("ticket: Config.%s is %q, under %s/, which the framework keeps for itself", prefix.name, prefix.value, reservedPrefix)
		}
	}
	if err := c.Paths.validate(); err != nil {
		return err
	}
	for i, mw := range c.Middleware {
		if mw == nil {
			return fmt.Errorf("ticket: Config.Middleware[%d] is nil", i)
		}
	}
	for i, mw := range c.TeamMiddleware {
		if mw == nil {
			return fmt.Errorf("ticket: Config.TeamMiddleware[%d] is nil", i)
		}
	}
	if c.PageSize < 0 || c.PageSize > MaxPageSize {
		return fmt.Errorf("ticket: Config.PageSize is %d, and has to be between 0 and %d, where 0 means %d", c.PageSize, MaxPageSize, DefaultPageSize)
	}
	if c.MaxAttachmentBytes < 0 || c.MaxAttachmentBytes > MaxAttachmentBytesLimit {
		return fmt.Errorf("ticket: Config.MaxAttachmentBytes is %d, and has to be between 0 and %d, where 0 means %d", c.MaxAttachmentBytes, MaxAttachmentBytesLimit, DefaultMaxAttachmentBytes)
	}
	if c.MaxAttachments < 0 || c.MaxAttachments > MaxAttachmentsLimit {
		return fmt.Errorf("ticket: Config.MaxAttachments is %d, and has to be between 0 and %d, where 0 means %d", c.MaxAttachments, MaxAttachmentsLimit, DefaultMaxAttachments)
	}
	if c.ContactWindow < 0 || c.ContactWindow > MaxContactWindow {
		return fmt.Errorf("ticket: Config.ContactWindow is %s, and has to be between 0 and %s, where 0 means %s", c.ContactWindow, MaxContactWindow, DefaultContactWindow)
	}
	return validateRoutes(c.withDefaults())
}

// validateRoutes asks the standard library to parse the exact patterns the
// module will register, every one of them. Its parser is not exported and
// reports an invalid pattern, or two that collide, by panic, so the throwaway
// mux turns that boot-time panic into the configuration error New promises.
func validateRoutes(c Config) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("ticket: Config.Prefix %q, Config.TeamPrefix %q and Config.Paths cannot be registered as routes: %v", c.Prefix, c.TeamPrefix, recovered)
		}
	}()

	handler := http.NotFoundHandler()
	mux := http.NewServeMux()
	for _, route := range routeTable(c) {
		mux.Handle(route.method+" "+route.path, handler)
	}
	return nil
}

// withDefaults returns the configuration with the optional fields filled in.
//
// It runs after Validate and never before: filling a default in first would
// hide the value somebody actually wrote from the check that would have refused
// it.
func (c Config) withDefaults() Config {
	if c.Prefix == "" {
		c.Prefix = DefaultPrefix
	}
	if c.TeamPrefix == "" {
		c.TeamPrefix = DefaultTeamPrefix
	}
	c.Paths = c.Paths.withDefaults()
	if c.PageSize == 0 {
		c.PageSize = DefaultPageSize
	}
	if c.MaxAttachmentBytes == 0 {
		c.MaxAttachmentBytes = DefaultMaxAttachmentBytes
	}
	if c.MaxAttachments == 0 {
		c.MaxAttachments = DefaultMaxAttachments
	}
	if c.ContactWindow == 0 {
		c.ContactWindow = DefaultContactWindow
	}
	if c.Locale == "" {
		c.Locale = DefaultLocale
	}
	if c.Location == nil {
		if loc, err := time.LoadLocation(DefaultLocation); err == nil {
			c.Location = loc
		} else {
			c.Location = time.FixedZone("BRT", -3*60*60)
		}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// route is one line of the table this module registers.
type route struct {
	name   string
	method string
	path   string
	team   bool
}

// routeTable is the whole table, built from the prefixes and the segments. It
// is read by validateRoutes, which registers it on a throwaway mux, and by the
// router.
func routeTable(c Config) []route {
	p, t, s := c.Prefix, c.TeamPrefix, c.Paths
	return []route{
		{"ticket.index", http.MethodGet, p, false},
		{"ticket.create", http.MethodGet, p + s.New, false},
		{"ticket.store", http.MethodPost, p, false},
		{"ticket.show", http.MethodGet, p + "/{id}", false},
		{"ticket.reply", http.MethodPost, p + "/{id}" + s.Replies, false},
		{"ticket.close", http.MethodPost, p + "/{id}" + s.Closure, false},
		{"ticket.reopen", http.MethodPost, p + "/{id}" + s.Reopening, false},
		{"ticket.attachment", http.MethodGet, p + "/{id}" + s.Attachments + "/{attachment}", false},

		{"ticket.team.index", http.MethodGet, t, true},
		{"ticket.team.show", http.MethodGet, t + "/{id}", true},
		{"ticket.team.reply", http.MethodPost, t + "/{id}" + s.Replies, true},
		{"ticket.team.note", http.MethodPost, t + "/{id}" + s.Notes, true},
		{"ticket.team.status", http.MethodPost, t + "/{id}" + s.Status, true},
		{"ticket.team.priority", http.MethodPost, t + "/{id}" + s.Priority, true},
		{"ticket.team.assign", http.MethodPost, t + "/{id}" + s.Assignee, true},
		{"ticket.team.attachment", http.MethodGet, t + "/{id}" + s.Attachments + "/{attachment}", true},
	}
}
