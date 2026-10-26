package feature_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	hedatabase "github.com/arandu-io/hesape/database"
	sqliteconnector "github.com/arandu-io/hesape/database/connectors/sqlite"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/filesystem"
	activitylog "github.com/hyz-is/arandu-activitylog"

	ticket "github.com/hyz-is/arandu-ticket"
)

// dsn opens a database file the way the tests need it: the write lock taken
// when a transaction begins, readers beside a writer, and a wait instead of an
// error when the lock is held.
const dsn = "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(20000)"

// appKey is the key a session store is built over. Any thirty-two bytes will
// do here; a real application reads its own from the environment.
const appKey = "0123456789abcdef0123456789abcdef"

// realm is the tenant the platform team signs in to.
const realm = "platform"

// signedIn are the subjects the tests started sessions for, by tenant and
// identifier: what a session store's resolver answers, as an application's
// reads the account's roles and actions again on every request.
var signedIn sync.Map

// resolveSubjects is the session store's resolver for these tests.
func resolveSubjects(_ context.Context, who security.Subject) (security.Subject, error) {
	if s, ok := signedIn.Load(who.Tenant + "/" + who.ID); ok {
		return s.(security.Subject), nil
	}
	return who, nil
}

// remember makes a subject the one the resolver answers for its account.
func remember(s security.Subject) security.Subject {
	signedIn.Store(s.Tenant+"/"+s.ID, s)
	return s
}

// notifications records what the Notifier was told, and can be made to fail.
type notifications struct {
	mu     sync.Mutex
	events []ticket.Event
	fail   error
}

func (n *notifications) Notify(_ context.Context, e ticket.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, e)
	return n.fail
}

func (n *notifications) all() []ticket.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]ticket.Event(nil), n.events...)
}

// accounts is the application's AccountResolver: an address book.
type accounts map[string]ticket.Account

func (a accounts) Resolve(_ context.Context, email string) (ticket.Account, error) {
	if account, ok := a[email]; ok {
		return account, nil
	}
	return ticket.Account{}, ticket.ErrNoAccount
}

// directory is the application's Directory.
type directory struct {
	people map[string]ticket.Person
	team   []ticket.Person
}

func (d directory) Person(_ context.Context, _, id string) (ticket.Person, error) {
	if p, ok := d.people[id]; ok {
		return p, nil
	}
	return ticket.Person{}, ticket.ErrNoAccount
}

func (d directory) Team(context.Context) ([]ticket.Person, error) { return d.team, nil }

// world is one application with this package installed over a real database.
type world struct {
	sql      *sql.DB
	conn     *hedatabase.Connection
	db       *data.DB
	sessions *security.SessionStore
	module   *ticket.Module
	tickets  *ticket.TicketService
	team     *ticket.TeamService
	notices  *notifications
	logs     *syncBuffer
	disk     *filesystem.Disk
	clock    *clock
}

// clock is a time the tests move by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// install opens a fresh SQLite database, applies the migrations of the log and
// of this package, and builds the module over it.
func install(t *testing.T) world {
	t.Helper()
	return installWith(t, nil)
}

// installWith is install with the module's configuration changed first.
func installWith(t *testing.T, change func(*ticket.Config)) world {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ticket.sqlite")
	handle, err := sql.Open(sqliteconnector.SQLiteConnector{}.DriverName(), "file:"+path+dsn)
	if err != nil {
		t.Fatalf("opening SQLite: %v", err)
	}
	handle.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = handle.Close() })

	connection := hedatabase.NewConnection(handle, path, "", map[string]any{
		"driver": string(hedatabase.DialectSQLite),
		"name":   "ticket-test",
	})
	db := data.Wrap(handle, data.DialectSQLite)
	sessions := security.NewSessionStore([]byte(appKey), time.Hour, false, security.NewMemoryBackend())
	sessions.ResolveSubjects(resolveSubjects)

	activity, err := activitylog.New(activitylog.Config{Tenant: realm, PlatformPolicy: nil}, db, sessions)
	if err != nil {
		t.Fatalf("building arandu-activitylog: %v", err)
	}
	adapter, err := filesystem.NewLocalFilesystemAdapter(t.TempDir())
	if err != nil {
		t.Fatalf("building a local Disk: %v", err)
	}
	disk := filesystem.NewDisk("tickets", adapter)

	notices := &notifications{}
	logs := &syncBuffer{}
	held := &clock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	cfg := ticket.Config{
		TeamTenant: realm,
		Notifier:   notices,
		Activity:   activity.Logger(),
		Files:      disk,
		Now:        held.Now,
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
	}
	if change != nil {
		change(&cfg)
	}
	module, err := ticket.New(cfg, db, sessions)
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	migrations := hedatabase.ForMigrations(connection)
	for _, migration := range append(activity.Migrations(), module.Migrations()...) {
		if err := migration.Up(context.Background(), migrations); err != nil {
			t.Fatalf("applying %s: %v", migration.GetName(), err)
		}
	}
	return world{
		sql: handle, conn: connection, db: db, sessions: sessions, module: module,
		tickets: module.Tickets(), team: module.Team(), notices: notices, logs: logs,
		disk: disk, clock: held,
	}
}

// The subjects of the tests. A customer holds the customer's actions; the
// team member holds the team's. Roles are written on them to prove the policy
// does not read them: a role opens nothing.

// member is a customer of a space with the actions of someone who asks for
// help: their own tickets, nothing of the rest of the space.
func member(tenant, id string) security.Subject {
	return security.Subject{ID: id, Tenant: tenant, Verified: true, Actions: []security.Action{
		ticket.TicketCreate, ticket.TicketViewOwn, ticket.TicketReply, ticket.TicketClose,
	}}
}

// owner is a customer who also reads the whole space's tickets.
func owner(tenant, id string) security.Subject {
	s := member(tenant, id)
	s.Actions = append(s.Actions, ticket.TicketViewSpace)
	return s
}

// agent is a member of the platform team with every action of the team.
func agent(id string) security.Subject {
	return security.Subject{ID: id, Tenant: realm, Verified: true, Roles: []string{"admin"}, Actions: ticket.TeamActions()}
}

// open opens a ticket as the subject and fails the test if it cannot.
func open(t *testing.T, w world, who security.Subject, subject, body string) *ticket.Ticket {
	t.Helper()
	record, err := w.tickets.Open(context.Background(), who, ticket.OpenRequest{Subject: subject, Body: body}, nil)
	if err != nil {
		t.Fatalf("opening %q: %v", subject, err)
	}
	return record
}

// requireErr fails unless err wraps want.
func requireErr(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

// entry is a row of the activity log, as the tests read it.
type entry struct {
	Event, Description, Causer, Subject, Properties string
}

// entries are the rows the log holds for a tenant, oldest first. They are read
// with a statement of the test's own: the log's policy denies reading by
// default, and what these tests ask is only what was written.
func entries(t *testing.T, w world, tenant string) []entry {
	t.Helper()
	rows, err := w.sql.Query(`SELECT COALESCE(event, ''), description, COALESCE(causer_id, ''), COALESCE(subject_id, ''), COALESCE(properties, '')
		FROM activity_log WHERE tenant_id = ? ORDER BY created_at, id`, tenant)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	defer rows.Close()
	var out []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Event, &e.Description, &e.Causer, &e.Subject, &e.Properties); err != nil {
			t.Fatalf("reading a log row: %v", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return out
}

// events are the names of the entries of a tenant.
func events(t *testing.T, w world, tenant string) []string {
	t.Helper()
	var out []string
	for _, e := range entries(t, w, tenant) {
		out = append(out, e.Event)
	}
	return out
}

// mustJSON is a conversation as text.
func mustJSON(t *testing.T, conv ticket.Conversation) []byte {
	t.Helper()
	body, err := json.Marshal(conv)
	if err != nil {
		t.Fatalf("encoding the response: %v", err)
	}
	return body
}

// syncBuffer is a log the tests read what the package could not return.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// hedatabaseFor is the connection a migration is run on.
func hedatabaseFor(w world) migrations.Connection { return hedatabase.ForMigrations(w.conn) }
