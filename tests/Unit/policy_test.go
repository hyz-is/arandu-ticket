package unit_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/database/query"

	ticket "github.com/hyz-is/arandu-ticket"
)

// The properties this package exists to keep are checked here, and they are
// checked against the code rather than described in a document:
//
//  1. the policies deny every action, and open one only for the subject who was
//     granted it, in the tenant the record is in;
//  2. the services authorize before constructing or running the generated query;
//  3. the tenant comes from the Grant;
//  4. nothing reaches the database without passing through the first two.
//
// The fourth is checked by the handle these tests pass in. It wraps a nil
// *sql.DB, so any statement that were issued would panic and fail the test
// loudly -- which makes "the refusal happened before the Model" a fact the
// suite proves rather than a comment. The structural twin in audit_test.go
// keeps that order visible on every service method, including an allowed path.

// customerActions are the actions of the customer's side, and teamActions the
// team's: every one the two policies answer about.
var (
	customerActions = ticket.Actions()
	teamActions     = ticket.TeamActions()
	everyAction     = append(append([]security.Action(nil), customerActions...), teamActions...)
)

// administrator is the most privileged subject an application could produce
// without granting it an action. It is the one to test the default with: a
// policy that refuses an administrator who holds no action refuses everyone.
func administrator(tenant string) security.Subject {
	return security.Subject{ID: "user-1", Tenant: tenant, Roles: []string{"admin", "owner", "ticket.admin"}, Verified: true}
}

// granted is a subject who holds exactly these actions.
func granted(tenant, id string, actions ...security.Action) security.Subject {
	return security.Subject{ID: id, Tenant: tenant, Verified: true, Actions: actions}
}

func TestThePoliciesDenyEveryActionByDefault(t *testing.T) {
	t.Parallel()

	for _, action := range append(slices.Clone(everyAction), ticket.TicketOpenFromContact) {
		for name, policy := range map[string]security.Policy[ticket.Ticket]{
			"customer": ticket.TicketPolicy{},
			"team":     ticket.TeamPolicy{Realm: "platform"},
		} {
			for tenant := range map[string]bool{"acme": true, "platform": true} {
				_, err := security.Authorize(context.Background(), policy, administrator(tenant), action, ticket.Ticket{})
				if !errors.Is(err, security.ErrForbidden) {
					t.Errorf("the %s policy allowed %s to an administrator of %s who was granted nothing: %v", name, action, tenant, err)
				}
			}
		}
	}
}

func TestNobodyHoldsTheContactFormsAction(t *testing.T) {
	t.Parallel()

	// Opening from the contact form is the system's, issued by the one function
	// that does. A subject who carries the action is still refused it by both
	// policies, so no person can ask for it.
	holder := granted("platform", "u", ticket.TicketOpenFromContact)
	for _, policy := range []security.Policy[ticket.Ticket]{ticket.TicketPolicy{}, ticket.TeamPolicy{Realm: "platform"}} {
		if _, err := security.Authorize(context.Background(), policy, holder, ticket.TicketOpenFromContact, ticket.Ticket{}); !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("a policy allowed a person the contact form's action: %v", err)
		}
	}
	for _, list := range [][]security.Action{customerActions, teamActions} {
		if slices.Contains(list, ticket.TicketOpenFromContact) {
			t.Fatal("the contact form's action is offered to a catalogue")
		}
	}
}

func TestTheTwoSetsOfActionsAreApart(t *testing.T) {
	t.Parallel()

	for _, action := range customerActions {
		if slices.Contains(teamActions, action) {
			t.Errorf("%s is in both sets, so a catalogue an organization grants from could hold a team action", action)
		}
		if !strings.HasPrefix(string(action), "ticket.") {
			t.Errorf("%s is not named after the package", action)
		}
	}
	for _, action := range teamActions {
		if !strings.HasPrefix(string(action), "ticket.") {
			t.Errorf("%s is not named after the package", action)
		}
	}
	if len(everyAction) != 9 {
		t.Errorf("%d actions are declared; the release notes name nine", len(everyAction))
	}
}

func TestACustomerIsAllowedWhatTheyWereGrantedAndNothingMore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	own := ticket.Ticket{ID: "t-1", TenantID: "acme", RequesterID: "user-ana"}
	others := ticket.Ticket{ID: "t-2", TenantID: "acme", RequesterID: "user-bia"}
	policy := ticket.TicketPolicy{}

	for _, c := range []struct {
		name   string
		who    security.Subject
		action security.Action
		on     ticket.Ticket
		want   bool
	}{
		{"create, granted", granted("acme", "user-ana", ticket.TicketCreate), ticket.TicketCreate, ticket.Ticket{}, true},
		{"create, not granted", granted("acme", "user-ana", ticket.TicketViewOwn), ticket.TicketCreate, ticket.Ticket{}, false},
		{"create, on a stored ticket", granted("acme", "user-ana", ticket.TicketCreate), ticket.TicketCreate, own, false},
		{"view own, the probe", granted("acme", "user-ana", ticket.TicketViewOwn), ticket.TicketViewOwn, ticket.Ticket{}, true},
		{"view own, theirs", granted("acme", "user-ana", ticket.TicketViewOwn), ticket.TicketViewOwn, own, true},
		{"view own, somebody else's", granted("acme", "user-ana", ticket.TicketViewOwn), ticket.TicketViewOwn, others, false},
		{"view own, another space's", granted("acme", "user-ana", ticket.TicketViewOwn), ticket.TicketViewOwn, ticket.Ticket{ID: "t-3", TenantID: "globex", RequesterID: "user-ana"}, false},
		{"view space, somebody else's", granted("acme", "user-boss", ticket.TicketViewSpace), ticket.TicketViewSpace, others, true},
		{"view space, another space's", granted("acme", "user-boss", ticket.TicketViewSpace), ticket.TicketViewSpace, ticket.Ticket{ID: "t-3", TenantID: "globex"}, false},
		{"reply, theirs", granted("acme", "user-ana", ticket.TicketReply), ticket.TicketReply, own, true},
		{"reply, somebody else's", granted("acme", "user-ana", ticket.TicketReply), ticket.TicketReply, others, false},
		{"reply, somebody else's, with the space", granted("acme", "user-boss", ticket.TicketReply, ticket.TicketViewSpace), ticket.TicketReply, others, true},
		{"reply, with only the right to read", granted("acme", "user-ana", ticket.TicketViewOwn), ticket.TicketReply, own, false},
		{"close, theirs", granted("acme", "user-ana", ticket.TicketClose), ticket.TicketClose, own, true},
		{"close, somebody else's", granted("acme", "user-ana", ticket.TicketClose), ticket.TicketClose, others, false},
		{"a team action", granted("acme", "user-ana", ticket.TicketManage), ticket.TicketManage, own, false},
	} {
		err := policy.Can(ctx, c.who, c.action, c.on)
		if (err == nil) != c.want {
			t.Errorf("%s: got %v, want allowed=%v", c.name, err, c.want)
		}
	}
}

func TestThePolicyDeniesARecordOfAnotherTenant(t *testing.T) {
	t.Parallel()

	other := ticket.Ticket{ID: "record-1", TenantID: "globex", RequesterID: "user-1"}
	everything := granted("acme", "user-1", customerActions...)

	for _, action := range customerActions {
		err := ticket.TicketPolicy{}.Can(context.Background(), everything, action, other)
		if err == nil {
			t.Fatalf("the policy allowed %s on a record belonging to another tenant", action)
		}
		// The message is asserted because the tenant check is the one refusal
		// that has to survive somebody opening the actions below it.
		if !strings.Contains(err.Error(), "another tenant") {
			t.Fatalf("the refusal did not name the tenant: %v", err)
		}
	}
}

func TestThePolicyDeniesAGuestAndNobody(t *testing.T) {
	t.Parallel()

	for _, action := range everyAction {
		for name, policy := range map[string]security.Policy[ticket.Ticket]{"customer": ticket.TicketPolicy{}, "team": ticket.TeamPolicy{Realm: "acme"}} {
			if _, err := security.Authorize(context.Background(), policy, security.Guest("acme"), action, ticket.Ticket{}); !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("the %s policy allowed a guest %s: %v", name, action, err)
			}
			// The zero Subject is a session that failed to load, not an
			// anonymous reader, and it is refused before the policy is consulted.
			if _, err := security.Authorize(context.Background(), policy, security.Subject{}, action, ticket.Ticket{}); !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("the %s policy allowed the empty subject %s: %v", name, action, err)
			}
		}
	}
}

func TestTheTeamPolicyAllowsTheTeamAndOnlyTheTeam(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	policy := ticket.TeamPolicy{Realm: "platform"}
	inAnyTenant := ticket.Ticket{ID: "t-1", TenantID: "globex", RequesterID: "user-gil"}

	// A member of the team reads and acts on a ticket of any tenant: crossing
	// tenants is what the team does.
	for _, action := range teamActions {
		if err := policy.Can(ctx, granted("platform", "team-1", action), action, inAnyTenant); err != nil {
			t.Errorf("a team member granted %s was refused it: %v", action, err)
		}
		// An action opens itself and no other.
		for _, other := range teamActions {
			if other != action && policy.Can(ctx, granted("platform", "team-1", action), other, inAnyTenant) == nil {
				t.Errorf("%s opened %s", action, other)
			}
		}
	}

	// And the customer's actions are not the team's.
	for _, action := range customerActions {
		if policy.Can(ctx, granted("platform", "team-1", action), action, inAnyTenant) == nil {
			t.Errorf("the team policy answered the customer's action %s", action)
		}
	}

	all := granted("platform", "team-1", teamActions...)
	cases := map[string]security.Subject{
		"a subject of another tenant": granted("acme", "team-1", teamActions...),
		"a subject viewing as somebody": func() security.Subject {
			s := all
			s.Impersonator = &security.Subject{ID: "operator"}
			return s
		}(),
	}
	for name, who := range cases {
		if err := policy.Can(ctx, who, ticket.TicketViewAll, inAnyTenant); err == nil {
			t.Errorf("the team policy allowed %s", name)
		}
	}
	// With no realm, nobody is the team.
	if err := (ticket.TeamPolicy{}).Can(ctx, granted("", "team-1", teamActions...), ticket.TicketViewAll, inAnyTenant); err == nil {
		t.Error("a policy with no realm allowed somebody")
	}
}

func TestTheGrantsOfReadingOnlyReadAndWhatWritesWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, c := range []struct {
		policy security.Policy[ticket.Ticket]
		who    security.Subject
		action security.Action
		writes bool
	}{
		{ticket.TicketPolicy{}, granted("acme", "u", ticket.TicketViewOwn), ticket.TicketViewOwn, false},
		{ticket.TicketPolicy{}, granted("acme", "u", ticket.TicketViewSpace), ticket.TicketViewSpace, false},
		{ticket.TicketPolicy{}, granted("acme", "u", ticket.TicketCreate), ticket.TicketCreate, true},
		{ticket.TicketPolicy{}, granted("acme", "u", ticket.TicketReply), ticket.TicketReply, true},
		{ticket.TicketPolicy{}, granted("acme", "u", ticket.TicketClose), ticket.TicketClose, true},
		{ticket.TeamPolicy{Realm: "platform"}, granted("platform", "u", ticket.TicketViewAll), ticket.TicketViewAll, false},
		{ticket.TeamPolicy{Realm: "platform"}, granted("platform", "u", ticket.TicketManage), ticket.TicketManage, true},
		{ticket.TeamPolicy{Realm: "platform"}, granted("platform", "u", ticket.TicketAssign), ticket.TicketAssign, true},
		{ticket.TeamPolicy{Realm: "platform"}, granted("platform", "u", ticket.TicketNote), ticket.TicketNote, true},
	} {
		g, err := security.Authorize(ctx, c.policy, c.who, c.action, ticket.Ticket{})
		if err != nil {
			t.Fatalf("%s: %v", c.action, err)
		}
		// A Grant issued to read cannot write: the database refuses it.
		if got := auth.Writable(g) == nil; got != c.writes {
			t.Errorf("a Grant issued for %s is writable = %v, want %v", c.action, got, c.writes)
		}
	}
}

// nilHandle is a handle over no database.
//
// Any statement issued through it panics, which is what makes these tests
// prove that the refusal came first: a service that reached the Model before
// authorizing would crash here rather than pass.
func nilHandle() *data.DB { return data.Wrap(nil, data.DialectSQLite) }

func TestTheServicesRefuseBeforeReachingTheModel(t *testing.T) {
	t.Parallel()

	// A nil handle makes even construction of Tickets panic, at the
	// GetQueryGrammar the query asks the handle for. This catches moving the
	// generated query constructor -- not only its terminal -- ahead of
	// authorization.
	cfg := ticket.Config{TeamTenant: "platform"}
	customers := ticket.NewTicketService(nil, cfg)
	team := ticket.NewTeamService(nil, cfg)
	ctx := context.Background()
	nobody := administrator("acme")
	nobodyOfTheTeam := administrator("platform")

	for name, err := range map[string]error{
		"Find":    second(customers.Find(ctx, nobody, "t-1")),
		"List":    second(customers.List(ctx, nobody, ticket.ListQuery{})),
		"Open":    second(customers.Open(ctx, nobody, ticket.OpenRequest{Subject: "a", Body: "b"}, nil)),
		"Reply":   second(customers.Reply(ctx, nobody, "t-1", ticket.ReplyRequest{Body: "b"}, nil)),
		"Close":   second(customers.Close(ctx, nobody, "t-1")),
		"Reopen":  second(customers.Reopen(ctx, nobody, "t-1")),
		"CanOpen": customers.CanOpen(ctx, nobody),

		"team Find":     second(team.Find(ctx, nobodyOfTheTeam, "t-1")),
		"team List":     second(team.List(ctx, nobodyOfTheTeam, ticket.TeamQuery{})),
		"team Reply":    second(team.Reply(ctx, nobodyOfTheTeam, "t-1", ticket.ReplyRequest{Body: "b"}, nil)),
		"team Note":     second(team.Note(ctx, nobodyOfTheTeam, "t-1", ticket.ReplyRequest{Body: "b"}, nil)),
		"team Status":   second(team.SetStatus(ctx, nobodyOfTheTeam, "t-1", ticket.StatusClosed)),
		"team Priority": second(team.SetPriority(ctx, nobodyOfTheTeam, "t-1", ticket.PriorityHigh)),
		"team Assign":   second(team.Assign(ctx, nobodyOfTheTeam, "t-1", "u")),
		"team Team":     second(team.Team(ctx, nobodyOfTheTeam)),
	} {
		if !errors.Is(err, security.ErrForbidden) {
			t.Errorf("%s reached the Model before the policy refusal: %v", name, err)
		}
	}
	if _, _, err := customers.Attachment(ctx, nobody, "t-1", "a-1"); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("Attachment reached the Model before the policy refusal: %v", err)
	}
	if _, _, err := team.Attachment(ctx, nobodyOfTheTeam, "t-1", "a-1"); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("team Attachment reached the Model before the policy refusal: %v", err)
	}
}

// second is the error of a call that answers a value and an error.
func second[T any](_ T, err error) error { return err }

// recordingHandle runs nothing and keeps every statement it is handed, so a
// test reads what the table compiles instead of reading its settings. The table
// keeps its settings to itself; what it writes is the behaviour they decide.
type recordingHandle struct {
	*data.DB
	statements []recordedStatement
}

type recordedStatement struct {
	sql      string
	bindings []any
}

// newRecordingHandle borrows the grammar and the processor of an SQLite handle
// over no database, and answers every statement itself.
func newRecordingHandle() *recordingHandle { return &recordingHandle{DB: nilHandle()} }

func (h *recordingHandle) record(sql string, bindings []any) {
	h.statements = append(h.statements, recordedStatement{sql: sql, bindings: slices.Clone(bindings)})
}

func (h *recordingHandle) Select(_ context.Context, sql string, bindings []any, _ bool) ([]query.Record, error) {
	h.record(sql, bindings)
	return nil, nil
}

func (h *recordingHandle) Insert(_ context.Context, sql string, bindings []any) (bool, error) {
	h.record(sql, bindings)
	return true, nil
}

func (h *recordingHandle) Update(_ context.Context, sql string, bindings []any) (int64, error) {
	h.record(sql, bindings)
	return 1, nil
}

func (h *recordingHandle) Delete(_ context.Context, sql string, bindings []any) (int64, error) {
	h.record(sql, bindings)
	return 1, nil
}

func (h *recordingHandle) Statement(_ context.Context, sql string, bindings []any) (bool, error) {
	h.record(sql, bindings)
	return true, nil
}

// first is the first recorded statement that starts with verb.
func (h *recordingHandle) first(t *testing.T, verb string) recordedStatement {
	t.Helper()
	for _, statement := range h.statements {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(statement.sql)), verb) {
			return statement
		}
	}
	t.Fatalf("no %s statement was issued; recorded %v", verb, h.statements)
	return recordedStatement{}
}

// TestTicketsReturnsAWiredTenantScopedQuery holds what ticketTable declares,
// through what it makes the generated query do: the table it reads, a key the
// application writes and the engine never fills, and a tenant_id taken from the
// Grant on the way in and on the way out.
func TestTicketsReturnsAWiredTenantScopedQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	handle := newRecordingHandle()
	table := ticket.Tickets(handle).Base().Table()
	if table.Name() != "tickets" {
		t.Fatalf("Tickets table = %q, want tickets", table.Name())
	}
	if key := table.MorphModel(handle); key.GetKeyName() != "id" || key.GetKeyType() != "string" {
		t.Fatalf("Tickets key is %q of type %q; want id, as text", key.GetKeyName(), key.GetKeyType())
	}

	row, err := ticket.Tickets(handle).New()
	if err != nil {
		t.Fatalf("building a row: %v", err)
	}
	if row.Table() != table {
		t.Fatal("Tickets returned an entity whose embedded Model is not wired to its table")
	}

	// The key is application-generated text: the insert carries the one the row
	// was given, and the engine is never asked for one.
	row.ID = "record-1"
	if _, err := row.Save(ctx, security.SystemGrant(ticket.TicketCreate, "acme")); err != nil {
		t.Fatalf("saving through the recording handle: %v", err)
	}
	insert := handle.first(t, "insert")
	if !slices.Contains(insert.bindings, any("record-1")) || row.ID != "record-1" {
		t.Fatalf("the insert %q %v does not write the application key, or the row lost it (%q)", insert.sql, insert.bindings, row.ID)
	}
	if !strings.Contains(insert.sql, "tenant_id") || !slices.Contains(insert.bindings, any("acme")) {
		t.Fatalf("the insert %q %v does not stamp tenant_id with the Grant's tenant", insert.sql, insert.bindings)
	}

	// And the tenant column scopes a read by the Grant's tenant, for each of the
	// three tables a customer's statement reaches.
	grant := security.SystemGrant(ticket.TicketViewOwn, "acme")
	for name, run := range map[string]func() error{
		"tickets":     func() error { _, err := ticket.Tickets(handle).WhereKey("record-1").First(ctx, grant); return err },
		"messages":    func() error { _, err := ticket.Messages(handle).WhereKey("record-1").First(ctx, grant); return err },
		"attachments": func() error { _, err := ticket.Attachments(handle).WhereKey("record-1").First(ctx, grant); return err },
	} {
		handle.statements = nil
		if err := run(); err != nil {
			t.Fatalf("reading %s through the recording handle: %v", name, err)
		}
		read := handle.first(t, "select")
		if !strings.Contains(read.sql, "tenant_id") || !slices.Contains(read.bindings, any("acme")) {
			t.Fatalf("the read of %s %q %v is not filtered by tenant_id with the Grant's tenant", name, read.sql, read.bindings)
		}
	}
}

// TestTheMissingRecordIsTheModelsNotFound holds the one link between this
// package's sentinel and the router's answer. The router answers
// model.ErrModelNotFound with 404 and has never heard of ErrNotFound, so the
// sentinel is a 404 only while it wraps the model's.
func TestTheMissingRecordIsTheModelsNotFound(t *testing.T) {
	t.Parallel()

	if !errors.Is(ticket.ErrNotFound, model.ErrModelNotFound) {
		t.Fatal("ErrNotFound does not wrap model.ErrModelNotFound, so the router would answer a missing record with 500")
	}
}

func TestASystemGrantWithoutATenantReachesNothing(t *testing.T) {
	t.Parallel()

	// A system grant with no tenant names no customer. The Model refuses it
	// while preparing the query, before the nil handle can issue a statement.
	_, err := ticket.Tickets(nilHandle()).WhereKey("record-1").First(
		context.Background(), security.SystemGrant(ticket.TicketViewOwn, ""))
	if !errors.Is(err, model.ErrNoTenant) {
		t.Fatalf("a system grant with no tenant returned %v, want ErrNoTenant", err)
	}
}

func TestTheTenantComesFromTheGrant(t *testing.T) {
	t.Parallel()

	g := security.SystemGrant(ticket.TicketViewOwn, "acme")
	if got := data.Tenant(g); got != "acme" {
		t.Fatalf("data.Tenant(g) = %q, want %q", got, "acme")
	}
	// And a Grant nobody issued carries no tenant at all, so a statement that
	// took its tenant from anywhere else would be reading rows this Grant does
	// not name.
	if got := data.Tenant(security.Grant{}); got != "" {
		t.Fatalf("the zero Grant carries the tenant %q, want none", got)
	}
}

func TestTheTicketsOwnRules(t *testing.T) {
	t.Parallel()

	now := mustTime("2026-10-26T12:00:00Z")
	later := now.Add(time.Hour)
	new := func(status ticket.Status) *ticket.Ticket {
		return &ticket.Ticket{Number: 123, Status: status, Priority: ticket.PriorityNormal}
	}

	if got := new(ticket.StatusOpen).Code(); got != "#000123" {
		t.Errorf("the code is %q", got)
	}
	if got := (&ticket.Ticket{Number: 1234567}).Code(); got != "#1234567" {
		t.Errorf("a number wider than the code is %q", got)
	}

	// What a customer's reply does, by status.
	for from, want := range map[ticket.Status]ticket.Status{
		ticket.StatusOpen: ticket.StatusOpen, ticket.StatusWaitingCustomer: ticket.StatusWaitingTeam,
		ticket.StatusWaitingTeam: ticket.StatusWaitingTeam, ticket.StatusResolved: ticket.StatusWaitingTeam,
	} {
		tk := new(from)
		if err := tk.ReceiveCustomerReply(now); err != nil || tk.Status != want || !tk.LastMessageAt.Equal(now) {
			t.Errorf("a customer's reply on %s gave %s (%v), want %s", from, tk.Status, err, want)
		}
	}
	// What the team's does.
	for _, from := range []ticket.Status{ticket.StatusOpen, ticket.StatusWaitingCustomer, ticket.StatusWaitingTeam, ticket.StatusResolved} {
		tk := new(from)
		if err := tk.ReceiveTeamReply(now); err != nil || tk.Status != ticket.StatusWaitingCustomer {
			t.Errorf("the team's reply on %s gave %s (%v)", from, tk.Status, err)
		}
	}
	// A closed ticket takes neither, and is left as it was.
	closed := new(ticket.StatusClosed)
	if closed.ReceiveCustomerReply(now) == nil || closed.ReceiveTeamReply(now) == nil || !closed.UpdatedAt.IsZero() {
		t.Error("a closed ticket took a reply, or was changed by refusing one")
	}

	// Closing and reopening keep ClosedAt honest.
	tk := new(ticket.StatusWaitingTeam)
	if err := tk.Close(now); err != nil || tk.Status != ticket.StatusClosed || tk.ClosedAt == nil || !tk.ClosedAt.Equal(now) {
		t.Fatalf("closing: %v, %+v", err, tk)
	}
	if tk.Close(later) == nil || !tk.ClosedAt.Equal(now) {
		t.Error("a closed ticket was closed again, or its closing moved")
	}
	if err := tk.Reopen(later); err != nil || tk.Status != ticket.StatusWaitingTeam || tk.ClosedAt != nil {
		t.Fatalf("reopening: %v, %+v", err, tk)
	}
	if tk.Reopen(later) == nil {
		t.Error("an open ticket was reopened")
	}
	if err := tk.SetStatus(ticket.StatusClosed, later); err != nil || tk.ClosedAt == nil {
		t.Errorf("closing as the team: %v", err)
	}
	if err := tk.SetStatus(ticket.StatusResolved, later); err != nil || tk.ClosedAt != nil {
		t.Errorf("taking it out of closed as the team: %v, %v", err, tk.ClosedAt)
	}
	if tk.SetStatus(ticket.StatusResolved, later) == nil || tk.SetStatus("bogus", later) == nil {
		t.Error("the same status, or one that is not, was taken")
	}

	// Priority, assignment and notes.
	if tk.SetPriority(ticket.PriorityNormal, later) == nil || tk.SetPriority("huge", later) == nil {
		t.Error("the same priority, or one that is not, was taken")
	}
	if err := tk.SetPriority(ticket.PriorityUrgent, later); err != nil || tk.Priority != ticket.PriorityUrgent {
		t.Errorf("prioritising: %v", err)
	}
	if err := tk.Assign("team-1", "Rita", later); err != nil || tk.AssigneeID != "team-1" || tk.AssigneeName != "Rita" {
		t.Errorf("assigning: %v", err)
	}
	if tk.Assign("team-1", "Rita", later) == nil {
		t.Error("the same assignee was taken")
	}
	if err := tk.Assign("", "left over", later); err != nil || tk.AssigneeID != "" || tk.AssigneeName != "" {
		t.Errorf("taking it back: %v, %q", err, tk.AssigneeName)
	}
	before := *tk
	if err := tk.AddNote(later.Add(time.Hour)); err != nil || tk.Status != before.Status || !tk.LastMessageAt.Equal(before.LastMessageAt) || tk.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("a note moved the ticket, or left it untouched: %+v", tk)
	}
}

func TestTheConfigurationRefusesWhatCannotWork(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]ticket.Config{
		"no team tenant":     {},
		"relative prefix":    {TeamTenant: "platform", Prefix: "tickets"},
		"page size too big":  {TeamTenant: "platform", PageSize: ticket.MaxPageSize + 1},
		"negative page size": {TeamTenant: "platform", PageSize: -1},
	} {
		if err := cfg.Validate(); err == nil {
			t.Errorf("the configuration with %s was accepted", name)
		}
	}

	// The configuration an application that changes nothing writes names the
	// team's tenant, and no other: every customer's tenant comes from the Grant.
	if err := (ticket.Config{TeamTenant: "platform"}).Validate(); err != nil {
		t.Fatalf("the smallest configuration was refused: %v", err)
	}
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
