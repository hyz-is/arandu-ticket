package feature_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"

	ticket "github.com/hyz-is/arandu-ticket"
)

func TestTheTeamListsEverySpacesTicketsAndFiltersThem(t *testing.T) {
	t.Parallel()
	w := installWith(t, func(c *ticket.Config) {
		c.Directory = directory{
			people: map[string]ticket.Person{"user-ana": {ID: "user-ana", Name: "Ana Souza", Email: "ana@acme.test"}, "user-gil": {ID: "user-gil", Name: "Gil Lopes", Email: "gil@globex.test"}},
			team:   []ticket.Person{{ID: "team-1", Name: "Rita"}, {ID: "team-2", Name: "Davi"}},
		}
	})
	ctx := context.Background()
	rita, davi := agent("team-1"), agent("team-2")

	a := open(t, w, member("acme", "user-ana"), "Câmera offline", "x")
	g := open(t, w, member("globex", "user-gil"), "Fatura em aberto", "x")
	if _, err := w.team.SetPriority(ctx, rita, g.ID, ticket.PriorityUrgent); err != nil {
		t.Fatalf("prioritising: %v", err)
	}
	if _, err := w.team.Assign(ctx, rita, a.ID, "team-2"); err != nil {
		t.Fatalf("assigning: %v", err)
	}

	page, err := w.team.List(ctx, rita, ticket.TeamQuery{Sort: "number", Dir: "asc"})
	if err != nil || page.Total != 2 {
		t.Fatalf("the team lists %d tickets, %v; want those of both spaces", page.Total, err)
	}
	// Both are number one in their space: the list says whose.
	if page.Rows[0].Number != 1 || page.Rows[1].Number != 1 || page.Rows[0].Tenant == page.Rows[1].Tenant {
		t.Fatalf("the rows are %+v; want #000001 of acme and #000001 of globex", page.Rows)
	}

	for name, c := range map[string]struct {
		q    ticket.TeamQuery
		want []string
	}{
		"by the requester's name":    {ticket.TeamQuery{Search: "ana souza"}, []string{a.ID}},
		"by the requester's mail":    {ticket.TeamQuery{Search: "GLOBEX.test"}, []string{g.ID}},
		"by the subject":             {ticket.TeamQuery{Search: "fatura"}, []string{g.ID}},
		"by the number":              {ticket.TeamQuery{Search: "#000001"}, []string{a.ID, g.ID}},
		"by priority":                {ticket.TeamQuery{Priorities: []ticket.Priority{ticket.PriorityUrgent}}, []string{g.ID}},
		"by assignee":                {ticket.TeamQuery{Assignee: "team-2"}, []string{a.ID}},
		"by nobody's":                {ticket.TeamQuery{Assignee: ticket.AssigneeNone}, []string{g.ID}},
		"by space":                   {ticket.TeamQuery{Tenant: "acme"}, []string{a.ID}},
		"by status":                  {ticket.TeamQuery{Statuses: []ticket.Status{ticket.StatusClosed}}, nil},
		"by priority, highest first": {ticket.TeamQuery{Sort: "priority"}, []string{g.ID, a.ID}},
		"by priority, lowest first":  {ticket.TeamQuery{Sort: "priority", Dir: "asc"}, []string{a.ID, g.ID}},
	} {
		page, err := w.team.List(ctx, davi, c.q)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got []string
		for _, row := range page.Rows {
			got = append(got, row.ID)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") && !sameSet(got, c.want) {
			t.Errorf("%s found %v, want %v", name, got, c.want)
		}
		if name == "by priority, highest first" || name == "by priority, lowest first" {
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("%s found %v in that order, want %v", name, got, c.want)
			}
		}
	}

	var errs validation.Errors
	if _, err := w.team.List(ctx, rita, ticket.TeamQuery{Tenant: "Bad/Tenant"}); !errors.As(err, &errs) {
		t.Fatalf("a space that cannot be a tenant answered %v", err)
	}
	if _, err := w.team.List(ctx, rita, ticket.TeamQuery{Sort: "requester_email"}); !errors.As(err, &errs) {
		t.Fatalf("a column outside the allowlist answered %v", err)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestTheTeamActsInTheSpaceOfTheTicketAndNeverElsewhere(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	rita := agent("team-1")

	a := open(t, w, member("acme", "user-ana"), "da Acme", "x")
	g := open(t, w, member("globex", "user-gil"), "da Globex", "x")

	reply, err := w.team.Reply(ctx, rita, g.ID, ticket.ReplyRequest{Body: "Olá, Gil."}, nil)
	if err != nil {
		t.Fatalf("replying: %v", err)
	}
	// The message is in the ticket's space, whatever space the member is in.
	if reply.TenantID != "globex" || reply.TicketID != g.ID {
		t.Fatalf("the reply is in %q on %q; want globex's ticket", reply.TenantID, reply.TicketID)
	}
	if conv, err := w.tickets.Find(ctx, member("acme", "user-ana"), a.ID); err != nil || len(conv.Messages) != 1 {
		t.Fatalf("a reply on another space's ticket reached Acme's: %+v, %v", conv.Messages, err)
	}

	if _, err := w.team.Find(ctx, rita, "no-such-ticket"); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("a ticket that does not exist answered %v", err)
	}
	// A member of the team reads any space's conversation through the team's
	// service, in full.
	conv, err := w.team.Find(ctx, rita, g.ID)
	if err != nil || len(conv.Messages) != 2 || conv.Ticket.Tenant != "globex" {
		t.Fatalf("the team reads %+v, %v", conv, err)
	}
	if got := strings.Join(events(t, w, "globex"), ","); got != "ticket.opened,ticket.replied" {
		t.Fatalf("the log of globex holds %q", got)
	}
	// The act is the team member's, written in the customer's space.
	last := entries(t, w, "globex")[1]
	if last.Causer != "team-1" || last.Subject != g.ID {
		t.Fatalf("the entry is by %q about %q, want team-1 about the ticket", last.Causer, last.Subject)
	}
}

func TestAnInternalNoteIsInvisibleToTheCustomerInEveryWay(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana, rita := member("acme", "user-ana"), agent("team-1")
	record := open(t, w, ana, "nota", "x")
	before := len(w.notices.all())

	note, err := w.team.Note(ctx, rita, record.ID, ticket.ReplyRequest{Body: "SEGREDO: cliente já foi estornado"}, nil)
	if err != nil || !bool(note.Internal) || note.Role != ticket.RoleTeam {
		t.Fatalf("writing the note: %+v, %v", note, err)
	}

	// The team reads it.
	conv, err := w.team.Find(ctx, rita, record.ID)
	if err != nil || len(conv.Messages) != 2 || !conv.Messages[1].Internal {
		t.Fatalf("the team reads %+v, %v; want the note", conv.Messages, err)
	}

	// The customer does not: not in the conversation, not in the JSON of it,
	// and nothing tells them it exists.
	conv, err = w.tickets.Find(ctx, ana, record.ID)
	if err != nil || len(conv.Messages) != 1 {
		t.Fatalf("the customer reads %d messages, %v; want the one they wrote", len(conv.Messages), err)
	}
	if strings.Contains(string(mustJSON(t, conv)), "SEGREDO") {
		t.Fatal("the customer's response carries the note")
	}

	// A note changes nothing the customer can read, and tells nobody.
	again, _ := w.tickets.Find(ctx, ana, record.ID)
	if again.Ticket.Status != ticket.StatusOpen || !again.Ticket.LastMessageAt.Equal(conv.Ticket.LastMessageAt) {
		t.Fatalf("a note moved the ticket: %s at %s", again.Ticket.Status, again.Ticket.LastMessageAt)
	}
	if got := len(w.notices.all()); got != before {
		t.Fatalf("a note was told to %d listeners", got-before)
	}

	// The log says a note was written and not what it says.
	for _, e := range entries(t, w, "acme") {
		if strings.Contains(e.Description, "SEGREDO") || strings.Contains(e.Properties, "SEGREDO") {
			t.Fatalf("the log carries the note: %+v", e)
		}
	}
	if got := strings.Join(events(t, w, "acme"), ","); got != "ticket.opened,ticket.note_added" {
		t.Fatalf("the log holds %q", got)
	}

	// Nobody who is not the team writes one.
	if _, err := w.team.Note(ctx, ana, record.ID, ticket.ReplyRequest{Body: "x"}, nil); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a customer wrote a note: %v", err)
	}
	// And a note is allowed on a ticket that is closed.
	if _, err := w.tickets.Close(ctx, ana, record.ID); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if _, err := w.team.Note(ctx, rita, record.ID, ticket.ReplyRequest{Body: "arquivado"}, nil); err != nil {
		t.Fatalf("a note on a closed ticket: %v", err)
	}
}

func TestOnlyTheTeamActsOnTheTeamsSide(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	record := open(t, w, member("acme", "user-ana"), "lado da equipe", "x")

	everyAct := func(who security.Subject) map[string]error {
		out := map[string]error{}
		_, out["list"] = w.team.List(ctx, who, ticket.TeamQuery{})
		_, out["find"] = w.team.Find(ctx, who, record.ID)
		_, out["reply"] = w.team.Reply(ctx, who, record.ID, ticket.ReplyRequest{Body: "x"}, nil)
		_, out["note"] = w.team.Note(ctx, who, record.ID, ticket.ReplyRequest{Body: "x"}, nil)
		_, out["status"] = w.team.SetStatus(ctx, who, record.ID, ticket.StatusResolved)
		_, out["priority"] = w.team.SetPriority(ctx, who, record.ID, ticket.PriorityHigh)
		_, out["assign"] = w.team.Assign(ctx, who, record.ID, who.ID)
		_, _, out["attachment"] = w.team.Attachment(ctx, who, record.ID, "any")
		_, out["people"] = w.team.Team(ctx, who)
		return out
	}

	// The team's actions on a subject of a customer's space are worth nothing,
	// whatever the space calls itself: only the realm's tenant is the team's.
	intruder := agent("team-1")
	intruder.Tenant = "acme"
	inside := agent("team-1")
	inside.Impersonator = &security.Subject{ID: "operator"}
	inside.Acting = true

	for name, who := range map[string]security.Subject{
		"a customer":                              member("acme", "user-ana"),
		"a customer who is an admin":              {ID: "user-ana", Tenant: "acme", Roles: []string{"admin"}, Actions: ticket.TeamActions()},
		"a team member of another tenant":         intruder,
		"a team member with a role and no action": {ID: "team-9", Tenant: realm, Roles: []string{"admin", "owner"}},
		"somebody acting as a customer":           inside,
		"a guest":                                 security.Guest(realm),
		"nobody":                                  {},
	} {
		for act, err := range everyAct(who) {
			if !errors.Is(err, security.ErrForbidden) {
				t.Errorf("%s: %s answered %v, want ErrForbidden", name, act, err)
			}
		}
	}

	// And each action opens what it names and nothing else.
	for _, c := range []struct {
		action security.Action
		act    string
		other  []string
	}{
		{ticket.TicketViewAll, "find", []string{"reply", "note", "status", "priority", "assign"}},
		{ticket.TicketManage, "reply", []string{"note", "assign"}},
		{ticket.TicketNote, "note", []string{"reply", "assign", "status"}},
		{ticket.TicketAssign, "assign", []string{"reply", "note", "status", "priority"}},
	} {
		who := security.Subject{ID: "team-5", Tenant: realm, Verified: true, Actions: []security.Action{c.action}}
		results := everyAct(who)
		if errors.Is(results[c.act], security.ErrForbidden) {
			t.Errorf("%s was refused %s", c.action, c.act)
		}
		for _, other := range c.other {
			if !errors.Is(results[other], security.ErrForbidden) {
				t.Errorf("%s allowed %s: %v", c.action, other, results[other])
			}
		}
	}
}

func TestTheTeamChangesStatusPriorityAndAssignee(t *testing.T) {
	t.Parallel()
	w := installWith(t, func(c *ticket.Config) {
		c.Directory = directory{team: []ticket.Person{{ID: "team-1", Name: "Rita"}, {ID: "team-2", Name: "Davi"}}}
	})
	ctx := context.Background()
	rita := agent("team-1")
	record := open(t, w, member("acme", "user-ana"), "mudar", "x")

	closed, err := w.team.SetStatus(ctx, rita, record.ID, ticket.StatusClosed)
	if err != nil || closed.Status != ticket.StatusClosed || closed.ClosedAt == nil {
		t.Fatalf("closing as the team: %+v, %v", closed, err)
	}
	waiting, err := w.team.SetStatus(ctx, rita, record.ID, ticket.StatusWaitingCustomer)
	if err != nil || waiting.ClosedAt != nil {
		t.Fatalf("taking it out of closed: %+v, %v", waiting, err)
	}
	var errs validation.Errors
	if _, err := w.team.SetStatus(ctx, rita, record.ID, ticket.StatusWaitingCustomer); !errors.As(err, &errs) {
		t.Fatalf("the same status answered %v", err)
	}
	if _, err := w.team.SetStatus(ctx, rita, record.ID, "bogus"); !errors.As(err, &errs) {
		t.Fatalf("a status that is not one answered %v", err)
	}

	high, err := w.team.SetPriority(ctx, rita, record.ID, ticket.PriorityHigh)
	if err != nil || high.Priority != ticket.PriorityHigh {
		t.Fatalf("prioritising: %+v, %v", high, err)
	}
	if _, err := w.team.SetPriority(ctx, rita, record.ID, ticket.PriorityHigh); !errors.As(err, &errs) {
		t.Fatalf("the same priority answered %v", err)
	}
	if _, err := w.team.SetPriority(ctx, rita, record.ID, "huge"); !errors.As(err, &errs) {
		t.Fatalf("a priority that is not one answered %v", err)
	}

	taken, err := w.team.Assign(ctx, rita, record.ID, "team-2")
	if err != nil || taken.AssigneeID != "team-2" || taken.AssigneeName != "Davi" {
		t.Fatalf("assigning: %+v, %v", taken, err)
	}
	if _, err := w.team.Assign(ctx, rita, record.ID, "team-2"); !errors.As(err, &errs) {
		t.Fatalf("the same assignee answered %v", err)
	}
	if _, err := w.team.Assign(ctx, rita, record.ID, "stranger"); !errors.As(err, &errs) || len(errs["assignee"]) == 0 {
		t.Fatalf("somebody who is not on the team was assigned: %v", err)
	}
	freed, err := w.team.Assign(ctx, rita, record.ID, "")
	if err != nil || freed.AssigneeID != "" || freed.AssigneeName != "" {
		t.Fatalf("taking it back: %+v, %v", freed, err)
	}

	want := "ticket.opened,ticket.closed,ticket.reopened,ticket.priority_changed,ticket.assigned,ticket.assigned"
	if got := strings.Join(events(t, w, "acme"), ","); got != want {
		t.Fatalf("the log holds\n%s\nwant\n%s", got, want)
	}
}

func TestWithoutADirectoryAMemberCanOnlyTakeATicketThemselves(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	rita := agent("team-1")
	record := open(t, w, member("acme", "user-ana"), "sozinha", "x")

	var errs validation.Errors
	if _, err := w.team.Assign(ctx, rita, record.ID, "team-2"); !errors.As(err, &errs) {
		t.Fatalf("giving it to somebody else with no directory answered %v", err)
	}
	if taken, err := w.team.Assign(ctx, rita, record.ID, "team-1"); err != nil || taken.AssigneeID != "team-1" {
		t.Fatalf("taking it oneself: %+v, %v", taken, err)
	}
}

func TestTheTeamsAnswerTellsTheCustomerAndAFailingNotifierChangesNothing(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana, rita := member("acme", "user-ana"), agent("team-1")
	record := open(t, w, ana, "avisar", "x")

	if _, err := w.team.Reply(ctx, rita, record.ID, ticket.ReplyRequest{Body: "Resolvido?"}, nil); err != nil {
		t.Fatalf("replying: %v", err)
	}
	sent := w.notices.all()
	last := sent[len(sent)-1]
	if last.Kind != ticket.EventTeamReplied || last.Kind.ForTeam() || last.Ticket.RequesterID != "user-ana" || last.Message.Role != ticket.RoleTeam {
		t.Fatalf("the notifier was told %+v; want the team's answer, for the requester", last)
	}

	// The customer's reply tells the team.
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "Sim."}, nil); err != nil {
		t.Fatalf("replying: %v", err)
	}
	sent = w.notices.all()
	last = sent[len(sent)-1]
	if last.Kind != ticket.EventCustomerReplied || !last.Kind.ForTeam() {
		t.Fatalf("the notifier was told %+v; want the customer's reply, for the team", last)
	}

	// A notifier that fails does not undo what it was told about.
	w.notices.mu.Lock()
	w.notices.fail = errors.New("smtp: connection refused")
	w.notices.mu.Unlock()
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "Ainda aqui."}, nil); err != nil {
		t.Fatalf("a failing notifier failed the reply: %v", err)
	}
	conv, _ := w.tickets.Find(ctx, ana, record.ID)
	if len(conv.Messages) != 4 {
		t.Fatalf("the conversation holds %d messages, want the four written", len(conv.Messages))
	}

	// It is logged, with what it was about and not what was said.
	logged := w.logs.String()
	if !strings.Contains(logged, "notifier failed") || !strings.Contains(logged, "smtp: connection refused") || strings.Contains(logged, "Ainda aqui") || strings.Contains(logged, "avisar") {
		t.Fatalf("the log of the process holds %q", logged)
	}
}
