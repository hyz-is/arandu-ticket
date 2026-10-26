package feature_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/framework/data"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/migrations"

	ticket "github.com/hyz-is/arandu-ticket"
)

// These tests drive the module the way an application does: build it, register
// its routes on a router, sign people in, and make requests. The screens are
// drawn from views the application compiles, so the pages themselves are held
// by tests/Unit; here every route answers the JSON a client asks for, and every
// form is posted the way a browser posts it.

// reservedPrefix is the namespace the framework keeps for itself: the health
// probe, the reload endpoint, the development console, the addressed assets.
const reservedPrefix = "/_arandu"

// site is the module registered on a router the way the kernel builds one.
type site struct {
	world
	router *fhttp.Router
	flash  *security.Flash
}

// serve registers the routes of a world's module.
func serve(t *testing.T, w world) site {
	t.Helper()
	flash := security.NewFlash([]byte(appKey), false)
	router := fhttp.NewRouter().WithFlash(flash)
	w.module.Routes(router.ForModule(w.module.Name()))
	return site{world: w, router: router, flash: flash}
}

// signIn starts a session for the subject and returns the cookies a browser
// would send back. Only the account is written to the session; the actions
// come back through the store's resolver, as an application's would.
func (s site) signIn(t *testing.T, subject security.Subject) []*http.Cookie {
	t.Helper()
	remember(subject)
	rec := httptest.NewRecorder()
	if _, err := s.sessions.Start(context.Background(), rec, security.Subject{ID: subject.ID, Tenant: subject.Tenant}); err != nil {
		t.Fatalf("starting a session: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("starting a session wrote no cookie")
	}
	return cookies
}

// request is one request to make.
type request struct {
	method  string
	target  string
	form    url.Values
	json    bool
	cookies []*http.Cookie
	body    *bytes.Buffer
	ctype   string
}

// answer makes one request and returns the recorder. A panic out of the router
// is a failure of the test: it is how an error nobody claimed arrives here.
func (s site) answer(t *testing.T, r request) (rec *httptest.ResponseRecorder) {
	t.Helper()
	var body *strings.Reader
	switch {
	case r.body != nil:
		body = strings.NewReader(r.body.String())
	case r.form != nil:
		body = strings.NewReader(r.form.Encode())
	default:
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(r.method, r.target, body)
	switch {
	case r.ctype != "":
		req.Header.Set("Content-Type", r.ctype)
	case r.form != nil:
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if r.json {
		req.Header.Set("Accept", "application/json")
	}
	for _, cookie := range r.cookies {
		req.AddCookie(cookie)
	}
	rec = httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("%s %s panicked, which a deployed router answers with 500: %v", r.method, r.target, recovered)
		}
	}()
	s.router.ServeHTTP(rec, req)
	return rec
}

// decode reads a JSON response into a map.
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("the response is not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	if data, ok := out["data"].(map[string]any); ok {
		return data
	}
	return out
}

// everyRoute is a request to each route the module registers, under the
// default prefixes. TestTheModuleRegistersItsRoutesUnderItsPrefixes fails when
// a route exists that this list does not reach.
var everyRoute = []struct {
	method string
	target string
	team   bool
}{
	{http.MethodGet, "/tickets", false},
	{http.MethodGet, "/tickets/new", false},
	{http.MethodPost, "/tickets", false},
	{http.MethodGet, "/tickets/t-1", false},
	{http.MethodPost, "/tickets/t-1/replies", false},
	{http.MethodPost, "/tickets/t-1/closure", false},
	{http.MethodPost, "/tickets/t-1/reopening", false},
	{http.MethodGet, "/tickets/t-1/attachments/a-1", false},

	{http.MethodGet, "/admin/tickets", true},
	{http.MethodGet, "/admin/tickets/t-1", true},
	{http.MethodPost, "/admin/tickets/t-1/replies", true},
	{http.MethodPost, "/admin/tickets/t-1/notes", true},
	{http.MethodPost, "/admin/tickets/t-1/status", true},
	{http.MethodPost, "/admin/tickets/t-1/priority", true},
	{http.MethodPost, "/admin/tickets/t-1/assignee", true},
	{http.MethodGet, "/admin/tickets/t-1/attachments/a-1", true},
}

func TestAVisitorWithNoSessionIsSentToSignIn(t *testing.T) {
	t.Parallel()
	s := serve(t, install(t))

	// RequireAuth answers before the handler runs, so a visitor with no
	// session reaches neither the policy nor the database.
	for _, r := range everyRoute {
		rec := s.answer(t, request{method: r.method, target: r.target})
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != middleware.SignInPath {
			t.Errorf("%s %s answered %d to %q, want %d to %s", r.method, r.target, rec.Code, rec.Header().Get("Location"), http.StatusSeeOther, middleware.SignInPath)
		}
	}
}

func TestACustomerCannotReachTheTeamsSideAndTheTeamCannotActAsACustomer(t *testing.T) {
	t.Parallel()
	// A form that passes every check, so what refuses is the policy and not the
	// validation that comes first.
	validForm := url.Values{"body": {"texto"}, "subject": {"assunto"}, "status": {"closed"}, "priority": {"high"}, "assignee": {"team-1"}}
	s := serve(t, install(t))
	customer := s.signIn(t, owner("acme", "user-boss"))
	team := s.signIn(t, agent("team-1"))

	for _, r := range everyRoute {
		if !r.team {
			continue
		}
		rec := s.answer(t, request{method: r.method, target: r.target, cookies: customer, json: true, form: validForm})
		if rec.Code != http.StatusForbidden {
			t.Errorf("a customer on %s %s was answered %d, want 403", r.method, r.target, rec.Code)
		}
	}
	// The team member holds none of the customer's actions: the customer's
	// side is not theirs, though the session is valid.
	for _, r := range everyRoute {
		if r.team || r.method != http.MethodGet || r.target == "/tickets/new" {
			continue
		}
		rec := s.answer(t, request{method: r.method, target: r.target, cookies: team, json: true, form: validForm})
		if rec.Code != http.StatusForbidden {
			t.Errorf("a team member on %s %s was answered %d, want 403", r.method, r.target, rec.Code)
		}
	}
}

func TestACustomerOpensReadsRepliesClosesAndReopensOverHTTP(t *testing.T) {
	t.Parallel()
	s := serve(t, install(t))
	ana := s.signIn(t, member("acme", "user-ana"))

	// Opening redirects to the conversation.
	rec := s.answer(t, request{method: http.MethodPost, target: "/tickets", cookies: ana,
		form: url.Values{"subject": {"Sem imagem"}, "category": {"Câmeras"}, "body": {"Tela preta desde ontem."}}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/tickets/") {
		t.Fatalf("opening answered %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	conversation := rec.Header().Get("Location")

	rec = s.answer(t, request{method: http.MethodGet, target: conversation, cookies: ana, json: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("reading answered %d: %s", rec.Code, rec.Body)
	}
	shown := decode(t, rec)
	tk := shown["ticket"].(map[string]any)
	if tk["code"] != "#000001" || tk["status"] != "open" || tk["subject"] != "Sem imagem" {
		t.Fatalf("the ticket is %v", tk)
	}
	// The customer is not shown the team's side of the ticket.
	for _, internal := range []string{"assignee_id", "assignee_name", "priority", "origin", "tenant", "tenant_id", "origin_key"} {
		if _, ok := tk[internal]; ok {
			t.Errorf("the customer's ticket carries %q", internal)
		}
	}
	messages := shown["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["body"] != "Tela preta desde ontem." {
		t.Fatalf("the messages are %v", messages)
	}
	if _, ok := messages[0].(map[string]any)["internal"]; ok {
		t.Fatal("the customer's message says whether it is internal")
	}

	// A reply, as a form and as JSON.
	rec = s.answer(t, request{method: http.MethodPost, target: conversation + "/replies", cookies: ana, form: url.Values{"body": {"Reiniciei o aparelho."}}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != conversation {
		t.Fatalf("replying answered %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = s.answer(t, request{method: http.MethodPost, target: conversation + "/replies", cookies: ana, json: true, form: url.Values{"body": {"E nada."}}})
	if rec.Code != http.StatusCreated || decode(t, rec)["body"] != "E nada." {
		t.Fatalf("replying for JSON answered %d: %s", rec.Code, rec.Body)
	}

	// The list.
	rec = s.answer(t, request{method: http.MethodGet, target: "/tickets?q=imagem&status=open&sort=number&dir=asc", cookies: ana, json: true})
	list := decode(t, rec)
	if rec.Code != http.StatusOK || list["total"].(float64) != 1 || len(list["items"].([]any)) != 1 {
		t.Fatalf("listing answered %d: %s", rec.Code, rec.Body)
	}
	// An address somebody edited by hand is not a form to send back.
	rec = s.answer(t, request{method: http.MethodGet, target: "/tickets?sort=tenant_id&status=bogus&page=-4", cookies: ana, json: true})
	if rec.Code != http.StatusOK || decode(t, rec)["total"].(float64) != 1 {
		t.Fatalf("a hand-edited address answered %d: %s", rec.Code, rec.Body)
	}

	// Close, and a reply to a closed ticket goes back with the reason.
	rec = s.answer(t, request{method: http.MethodPost, target: conversation + "/closure", cookies: ana, json: true})
	if rec.Code != http.StatusOK || decode(t, rec)["status"] != "closed" {
		t.Fatalf("closing answered %d: %s", rec.Code, rec.Body)
	}
	rec = s.answer(t, request{method: http.MethodPost, target: conversation + "/replies", cookies: ana, json: true, form: url.Values{"body": {"x"}}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "encerrado") {
		t.Fatalf("a reply to a closed ticket answered %d: %s", rec.Code, rec.Body)
	}
	rec = s.answer(t, request{method: http.MethodPost, target: conversation + "/reopening", cookies: ana, json: true})
	if rec.Code != http.StatusOK || decode(t, rec)["status"] != "waiting_team" {
		t.Fatalf("reopening answered %d: %s", rec.Code, rec.Body)
	}
}

func TestARejectedFormGoesBackWithTheMessagesInPortuguese(t *testing.T) {
	t.Parallel()
	s := serve(t, install(t))
	ana := s.signIn(t, member("acme", "user-ana"))

	rec := s.answer(t, request{method: http.MethodPost, target: "/tickets", cookies: ana, form: url.Values{"subject": {""}, "body": {""}}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("an empty form answered %d to %q, want %d back where it came from", rec.Code, rec.Header().Get("Location"), http.StatusSeeOther)
	}
	next := httptest.NewRequest(http.MethodGet, "/", nil)
	next.Header.Set("Accept", "text/html")
	for _, cookie := range rec.Result().Cookies() {
		next.AddCookie(cookie)
	}
	errs, _, ok := s.flash.Take(httptest.NewRecorder(), next)
	if !ok || len(errs["subject"]) == 0 || errs["subject"][0] != "Preencha este campo." {
		t.Fatalf("the redirect carries %v in the flash", errs)
	}

	rec = s.answer(t, request{method: http.MethodPost, target: "/tickets", cookies: ana, json: true, form: url.Values{"subject": {""}}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Preencha este campo.") {
		t.Fatalf("an empty form for JSON answered %d: %s", rec.Code, rec.Body)
	}
}

func TestATicketOfAnotherSpaceIsNotFoundOverHTTP(t *testing.T) {
	t.Parallel()
	w := install(t)
	s := serve(t, w)
	theirs := open(t, w, member("globex", "user-gil"), "da Globex", "x")
	ana := s.signIn(t, owner("acme", "user-boss"))

	for _, r := range []request{
		{method: http.MethodGet, target: "/tickets/" + theirs.ID},
		{method: http.MethodPost, target: "/tickets/" + theirs.ID + "/replies", form: url.Values{"body": {"oi"}}},
		{method: http.MethodPost, target: "/tickets/" + theirs.ID + "/closure"},
		{method: http.MethodPost, target: "/tickets/" + theirs.ID + "/reopening"},
		{method: http.MethodGet, target: "/tickets/" + theirs.ID + "/attachments/any"},
		{method: http.MethodGet, target: "/tickets/no-such-ticket"},
	} {
		r.cookies, r.json = ana, true
		if rec := s.answer(t, r); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want 404", r.method, r.target, rec.Code)
		}
	}
	// And nothing the request carries chooses the space: a tenant in the path,
	// the query or the body is a value the caller chose, and is read by nobody.
	rec := s.answer(t, request{method: http.MethodPost, target: "/tickets?tenant=globex&tenant_id=globex", cookies: ana, json: true,
		form: url.Values{"subject": {"Tentando"}, "body": {"x"}, "tenant": {"globex"}, "tenant_id": {"globex"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("opening answered %d: %s", rec.Code, rec.Body)
	}
	var tenant string
	if err := w.sql.QueryRow(`SELECT tenant_id FROM tickets WHERE subject = 'Tentando'`).Scan(&tenant); err != nil || tenant != "acme" {
		t.Fatalf("the ticket is in %q, %v; the tenant is the session's", tenant, err)
	}
}

func TestTheTeamReadsAnswersAndManagesOverHTTP(t *testing.T) {
	t.Parallel()
	w := install(t)
	s := serve(t, w)
	record := open(t, w, member("acme", "user-ana"), "Equipe", "Preciso de ajuda.")
	rita := s.signIn(t, agent("team-1"))
	base := "/admin/tickets/" + record.ID

	rec := s.answer(t, request{method: http.MethodGet, target: "/admin/tickets?q=equipe", cookies: rita, json: true})
	list := decode(t, rec)
	if rec.Code != http.StatusOK || list["total"].(float64) != 1 || list["items"].([]any)[0].(map[string]any)["tenant"] != "acme" {
		t.Fatalf("the team's list answered %d: %s", rec.Code, rec.Body)
	}

	for _, step := range []struct {
		target string
		form   url.Values
		field  string
		want   string
	}{
		{base + "/status", url.Values{"status": {"waiting_team"}}, "status", "waiting_team"},
		{base + "/priority", url.Values{"priority": {"urgent"}}, "priority", "urgent"},
		{base + "/assignee", url.Values{"assignee": {"team-1"}}, "assignee_id", "team-1"},
	} {
		rec = s.answer(t, request{method: http.MethodPost, target: step.target, cookies: rita, json: true, form: step.form})
		if rec.Code != http.StatusOK || decode(t, rec)[step.field] != step.want {
			t.Fatalf("%s answered %d: %s", step.target, rec.Code, rec.Body)
		}
	}
	rec = s.answer(t, request{method: http.MethodPost, target: base + "/replies", cookies: rita, json: true, form: url.Values{"body": {"Vamos olhar."}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("replying answered %d: %s", rec.Code, rec.Body)
	}
	rec = s.answer(t, request{method: http.MethodPost, target: base + "/notes", cookies: rita, json: true, form: url.Values{"body": {"NOTA-SECRETA"}}})
	if rec.Code != http.StatusCreated || decode(t, rec)["internal"] != true {
		t.Fatalf("the note answered %d: %s", rec.Code, rec.Body)
	}

	// The team reads the note; the customer's response does not carry it.
	rec = s.answer(t, request{method: http.MethodGet, target: base, cookies: rita, json: true})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "NOTA-SECRETA") {
		t.Fatalf("the team's conversation answered %d: %s", rec.Code, rec.Body)
	}
	ana := s.signIn(t, member("acme", "user-ana"))
	rec = s.answer(t, request{method: http.MethodGet, target: "/tickets/" + record.ID, cookies: ana, json: true})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "NOTA-SECRETA") || strings.Contains(rec.Body.String(), "assignee") || strings.Contains(rec.Body.String(), "urgent") {
		t.Fatalf("the customer's conversation answered %d: %s", rec.Code, rec.Body)
	}
	// Nor do the team's routes find a ticket that does not exist.
	rec = s.answer(t, request{method: http.MethodGet, target: "/admin/tickets/no-such-ticket", cookies: rita, json: true})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a ticket that does not exist answered %d", rec.Code)
	}
}

func TestAFileTravelsOverHTTPAndComesBackAsADownload(t *testing.T) {
	t.Parallel()
	w := install(t)
	s := serve(t, w)
	ana := s.signIn(t, member("acme", "user-ana"))

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("subject", "Com foto")
	_ = form.WriteField("body", "Veja a imagem.")
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="files"; filename="tela.png"`)
	header.Set("Content-Type", "text/html") // what the sender announced is not what it is
	part, _ := form.CreatePart(header)
	_, _ = part.Write(png)
	_ = form.Close()

	rec := s.answer(t, request{method: http.MethodPost, target: "/tickets", cookies: ana, json: true, body: &body, ctype: form.FormDataContentType()})
	if rec.Code != http.StatusCreated {
		t.Fatalf("opening with a file answered %d: %s", rec.Code, rec.Body)
	}
	id := decode(t, rec)["id"].(string)

	rec = s.answer(t, request{method: http.MethodGet, target: "/tickets/" + id, cookies: ana, json: true})
	files := decode(t, rec)["messages"].([]any)[0].(map[string]any)["attachments"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["content_type"] != "image/png" {
		t.Fatalf("the message carries %v", files)
	}
	file := files[0].(map[string]any)

	rec = s.answer(t, request{method: http.MethodGet, target: "/tickets/" + id + "/attachments/" + file["id"].(string), cookies: ana})
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("the download answered %d with %d bytes", rec.Code, rec.Body.Len())
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/png",
		"Content-Disposition":     `attachment; filename=tela.png`,
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, no-store",
		"Content-Security-Policy": "default-src 'none'; sandbox",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s is %q, want %q", header, got, want)
		}
	}
	// The address of a file holds no key, and never a public bucket's.
	if strings.Contains(rec.Body.String(), "ticket/attachments") {
		t.Fatal("the download carries a storage key")
	}
}

func TestTheModuleRegistersItsRoutesUnderItsPrefixes(t *testing.T) {
	t.Parallel()
	s := serve(t, install(t))

	got := make([]string, 0, len(everyRoute))
	for _, route := range s.router.Routes() {
		if route.Module != "ticket" {
			t.Errorf("the route %s %s is not tagged with the module name: %q", route.Method, route.Pattern, route.Module)
		}
		got = append(got, route.Method+" "+route.Pattern)
	}
	want := make([]string, 0, len(everyRoute))
	for _, r := range everyRoute {
		want = append(want, r.method+" "+routePattern(r.target))
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("registered\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The names are what a URL is built from.
	names := map[string]bool{}
	for _, route := range s.router.Routes() {
		names[route.RouteName()] = true
	}
	for _, name := range []string{"ticket.index", "ticket.show", "ticket.reply", "ticket.team.index", "ticket.team.note", "ticket.team.assign"} {
		if !names[name] {
			t.Errorf("no route is named %s", name)
		}
	}
}

// routePattern turns the sample identifiers of everyRoute into the wildcards the
// router registers.
func routePattern(target string) string {
	target = strings.ReplaceAll(target, "/t-1", "/{id}")
	return strings.ReplaceAll(target, "/a-1", "/{attachment}")
}

func TestThePrefixesAndTheSegmentsMoveTheRoutes(t *testing.T) {
	t.Parallel()
	w := installWith(t, func(c *ticket.Config) {
		c.Prefix, c.TeamPrefix, c.Paths = "/painel/chamados", "/admin/chamados", ticket.PortuguesePaths
	})
	s := serve(t, w)
	ana := s.signIn(t, member("acme", "user-ana"))

	rec := s.answer(t, request{method: http.MethodPost, target: "/painel/chamados", cookies: ana, json: true, form: url.Values{"subject": {"Em português"}, "body": {"x"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("opening under the new prefix answered %d: %s", rec.Code, rec.Body)
	}
	id := decode(t, rec)["id"].(string)
	if rec := s.answer(t, request{method: http.MethodPost, target: "/painel/chamados/" + id + "/respostas", cookies: ana, json: true, form: url.Values{"body": {"ok"}}}); rec.Code != http.StatusCreated {
		t.Fatalf("replying under the new segments answered %d: %s", rec.Code, rec.Body)
	}
	rita := s.signIn(t, agent("team-1"))
	if rec := s.answer(t, request{method: http.MethodPost, target: "/admin/chamados/" + id + "/responsavel", cookies: rita, json: true, form: url.Values{"assignee": {"team-1"}}}); rec.Code != http.StatusOK {
		t.Fatalf("assigning under the new segments answered %d: %s", rec.Code, rec.Body)
	}
	// What was at the old addresses is gone.
	if rec := s.answer(t, request{method: http.MethodGet, target: "/tickets", cookies: ana, json: true}); rec.Code != http.StatusNotFound {
		t.Fatalf("the old address answered %d, want 404", rec.Code)
	}

	// The URLs the screens link to follow the configuration.
	urls := w.module.URLs()
	if got := urls.Reply("abc"); got != "/painel/chamados/abc/respostas" {
		t.Fatalf("the reply address is %q", got)
	}
	if got := urls.TeamAttachment("abc", "f 1"); got != "/admin/chamados/abc/anexos/f%201" {
		t.Fatalf("the team's attachment address is %q", got)
	}
}

func TestTheTwoSidesAreMountedApartAndGuardedApart(t *testing.T) {
	t.Parallel()
	denied := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	}
	w := installWith(t, func(c *ticket.Config) { c.TeamMiddleware = []func(http.Handler) http.Handler{denied} })

	// Mounted alone, each side registers its own routes and no others.
	customerOnly := fhttp.NewRouter()
	w.module.CustomerRoutes(customerOnly.ForModule("ticket"))
	teamOnly := fhttp.NewRouter()
	w.module.TeamRoutes(teamOnly.ForModule("ticket"))
	for _, route := range customerOnly.Routes() {
		if strings.HasPrefix(route.Pattern, "/admin") {
			t.Errorf("the customer's routes include %s", route.Pattern)
		}
	}
	for _, route := range teamOnly.Routes() {
		if !strings.HasPrefix(route.Pattern, "/admin") {
			t.Errorf("the team's routes include %s", route.Pattern)
		}
	}
	if len(customerOnly.Routes()) == 0 || len(teamOnly.Routes()) == 0 {
		t.Fatal("a side registered no route, so this test proved nothing")
	}

	// A guard of the application's own side runs on that side alone.
	s := serve(t, w)
	rita := s.signIn(t, agent("team-1"))
	ana := s.signIn(t, member("acme", "user-ana"))
	if rec := s.answer(t, request{method: http.MethodGet, target: "/admin/tickets", cookies: rita, json: true}); rec.Code != http.StatusTeapot {
		t.Fatalf("the team's guard did not run: %d", rec.Code)
	}
	if rec := s.answer(t, request{method: http.MethodGet, target: "/tickets", cookies: ana, json: true}); rec.Code != http.StatusOK {
		t.Fatalf("the team's guard ran on the customer's side: %d", rec.Code)
	}
}

// TestNoRouteLandsInTheFrameworkNamespace is the one property of this package
// that syntax cannot hold: a prefix arrives through configuration, so the only
// way to know where the routes ended up is to register them and read the table
// back.
func TestNoRouteLandsInTheFrameworkNamespace(t *testing.T) {
	t.Parallel()

	for _, change := range []func(*ticket.Config){
		func(*ticket.Config) {},
		func(c *ticket.Config) { c.Prefix, c.TeamPrefix = "/painel/chamados", "/admin/chamados" },
	} {
		s := serve(t, installWith(t, change))
		registered := 0
		for _, route := range s.router.Routes() {
			registered++
			if route.Pattern == reservedPrefix || strings.HasPrefix(route.Pattern, reservedPrefix+"/") {
				t.Errorf("the route %s %s is registered under %s/, which the framework keeps for itself and refuses at boot", route.Method, route.Pattern, reservedPrefix)
			}
		}
		if registered == 0 {
			t.Fatal("the module registered no route, so this test proved nothing")
		}
	}
}

func TestNewRefusesAWiringThatCannotWork(t *testing.T) {
	t.Parallel()

	sessions := security.NewSessionStore([]byte(appKey), 3600e9, false, security.NewMemoryBackend())
	handle := data.Wrap(nil, data.DialectSQLite)
	valid := ticket.Config{TeamTenant: realm}

	for name, cfg := range map[string]ticket.Config{
		"no team tenant":            {},
		"a team tenant that is not": {TeamTenant: "Not/Valid"},
		"a relative prefix":         {TeamTenant: realm, Prefix: "tickets"},
		"a prefix in the framework": {TeamTenant: realm, TeamPrefix: "/_arandu/tickets"},
		"the same prefix twice":     {TeamTenant: realm, Prefix: "/x", TeamPrefix: "/x"},
		"a wildcard prefix":         {TeamTenant: realm, Prefix: "/tickets/{id}"},
		"a segment with a wildcard": {TeamTenant: realm, Paths: ticket.Paths{New: "/{new}"}},
		"a negative page size":      {TeamTenant: realm, PageSize: -1},
		"a page size too big":       {TeamTenant: realm, PageSize: ticket.MaxPageSize + 1},
		"files too big":             {TeamTenant: realm, MaxAttachmentBytes: ticket.MaxAttachmentBytesLimit + 1},
		"too many files":            {TeamTenant: realm, MaxAttachments: ticket.MaxAttachmentsLimit + 1},
		"a window too long":         {TeamTenant: realm, ContactWindow: ticket.MaxContactWindow + 1},
		"a nil guard":               {TeamTenant: realm, TeamMiddleware: []func(http.Handler) http.Handler{nil}},
	} {
		if _, err := ticket.New(cfg, handle, sessions); err == nil {
			t.Errorf("a configuration with %s was accepted", name)
		}
	}
	if _, err := ticket.New(valid, nil, sessions); err == nil {
		t.Error("a nil database handle was accepted")
	}
	if _, err := ticket.New(valid, handle, nil); err == nil {
		t.Error("a nil session store was accepted")
	}
	if _, err := ticket.New(valid, handle, sessions); err != nil {
		t.Fatalf("a valid wiring was refused: %v", err)
	}
}

func TestTheModuleDeclaresItsSchema(t *testing.T) {
	t.Parallel()
	w := install(t)

	declared := w.module.Migrations()
	if len(declared) == 0 {
		t.Fatal("the module declares migrations = true and returns none")
	}
	names := make([]string, 0, len(declared))
	for _, migration := range declared {
		name := migration.GetName()
		if name == "" {
			t.Fatal("a migration has no name, and the name is what carries the order")
		}
		names = append(names, name)
		// A migration that cannot be rolled back is a deploy that cannot be
		// undone. The migrator finds Down by type assertion, so a Down with the
		// wrong signature is a rollback that silently does nothing.
		if _, ok := migration.(migrations.ReversibleMigration); !ok {
			t.Errorf("the migration %s has no Down", name)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("the migrations are not returned in the order their names sort in: %v", names)
	}

	// Down takes it all back, and Up can run again.
	conn := hedatabaseFor(w)
	for _, migration := range declared {
		if err := migration.(migrations.ReversibleMigration).Down(context.Background(), conn); err != nil {
			t.Fatalf("rolling back %s: %v", migration.GetName(), err)
		}
	}
	var left int
	if err := w.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'ticket%'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d tables are left after the rollback, %v", left, err)
	}
	for _, migration := range declared {
		if err := migration.Up(context.Background(), conn); err != nil {
			t.Fatalf("applying %s again: %v", migration.GetName(), err)
		}
	}
}
