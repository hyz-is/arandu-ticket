package feature_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"

	ticket "github.com/hyz-is/arandu-ticket"
)

func TestACustomerOpensATicketAndReadsItBack(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana := member("acme", "user-ana")

	opened, err := w.tickets.Open(ctx, ana, ticket.OpenRequest{Subject: "  Câmera offline  ", Category: "Câmeras", Body: "Parou ontem às 14h."}, nil)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if opened.Code() != "#000001" || opened.Number != 1 {
		t.Fatalf("the first ticket of a space is %s (%d), want #000001", opened.Code(), opened.Number)
	}
	if opened.TenantID != "acme" || opened.RequesterID != "user-ana" {
		t.Fatalf("the ticket belongs to %q / %q; the tenant is the Grant's and the requester the subject", opened.TenantID, opened.RequesterID)
	}
	if opened.Status != ticket.StatusOpen || opened.Priority != ticket.PriorityNormal || opened.Origin != ticket.OriginWeb {
		t.Fatalf("a new ticket is %s / %s / %s", opened.Status, opened.Priority, opened.Origin)
	}
	if opened.Subject != "Câmera offline" || opened.Category != "Câmeras" {
		t.Fatalf("the subject %q and the category %q were not trimmed and kept", opened.Subject, opened.Category)
	}

	conv, err := w.tickets.Find(ctx, ana, opened.ID)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(conv.Messages) != 1 || conv.Messages[0].Body != "Parou ontem às 14h." || conv.Messages[0].Role != ticket.RoleCustomer {
		t.Fatalf("the conversation is %+v, want the first message of the customer", conv.Messages)
	}

	// The team is told, once, and so is the log.
	events := w.notices.all()
	if len(events) != 1 || events[0].Kind != ticket.EventOpened || events[0].Ticket.ID != opened.ID || events[0].Message.Body != "Parou ontem às 14h." {
		t.Fatalf("the notifier was told %+v, want one ticket.opened", events)
	}
	if got := strings.Join(eventNames(t, w, "acme"), ","); got != "ticket.opened" {
		t.Fatalf("the log holds %q, want ticket.opened", got)
	}
}

func eventNames(t *testing.T, w world, tenant string) []string { return events(t, w, tenant) }

func TestTicketNumbersAreSequentialInEachSpace(t *testing.T) {
	t.Parallel()
	w := install(t)

	a1 := open(t, w, member("acme", "user-ana"), "um", "x")
	a2 := open(t, w, member("acme", "user-bia"), "dois", "x")
	g1 := open(t, w, member("globex", "user-gil"), "um da outra", "x")
	a3 := open(t, w, member("acme", "user-ana"), "três", "x")

	for _, c := range []struct {
		got  *ticket.Ticket
		want string
	}{{a1, "#000001"}, {a2, "#000002"}, {g1, "#000001"}, {a3, "#000003"}} {
		if c.got.Code() != c.want {
			t.Errorf("%q is %s in %s, want %s", c.got.Subject, c.got.Code(), c.got.TenantID, c.want)
		}
	}
}

func TestTicketsOpenedAtTheSameTimeGetDifferentNumbers(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()

	const opens = 12
	numbers := make(chan int64, opens)
	var wg sync.WaitGroup
	for i := 0; i < opens; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record, err := w.tickets.Open(ctx, member("acme", fmt.Sprintf("user-%d", i)), ticket.OpenRequest{Subject: "ao mesmo tempo", Body: "x"}, nil)
			if err != nil {
				t.Errorf("opening: %v", err)
				return
			}
			numbers <- record.Number
		}()
	}
	wg.Wait()
	close(numbers)

	seen := map[int64]bool{}
	for n := range numbers {
		if seen[n] {
			t.Fatalf("the number %d was given twice", n)
		}
		seen[n] = true
	}
	for n := int64(1); n <= opens; n++ {
		if !seen[n] {
			t.Fatalf("the numbers %v leave a gap at %d", seen, n)
		}
	}
}

func TestTheInputIsValidatedBeforeAnythingIsAuthorized(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()

	var errs validation.Errors
	_, err := w.tickets.Open(ctx, member("acme", "user-ana"), ticket.OpenRequest{}, nil)
	if !errors.As(err, &errs) || len(errs["subject"]) == 0 || len(errs["body"]) == 0 {
		t.Fatalf("an empty request answered %v", err)
	}
	if errs["subject"][0] != "Preencha este campo." {
		t.Fatalf("the message is %q, want the sentence in Portuguese", errs["subject"][0])
	}
	_, err = w.tickets.Open(ctx, member("acme", "user-ana"), ticket.OpenRequest{Subject: strings.Repeat("a", 161), Body: "x"}, nil)
	if !errors.As(err, &errs) || len(errs["subject"]) == 0 {
		t.Fatalf("a subject past the column answered %v", err)
	}
	_, err = w.tickets.Open(ctx, member("acme", "user-ana"), ticket.OpenRequest{Subject: "ok", Body: strings.Repeat("é", ticket.MaxBodyChars+1)}, nil)
	if !errors.As(err, &errs) || len(errs["body"]) == 0 {
		t.Fatalf("a body past the limit answered %v", err)
	}
}

func TestACustomerSeesOnlyTheirOwnTicketsAndNeverAnotherSpaces(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana, bia, gil := member("acme", "user-ana"), member("acme", "user-bia"), member("globex", "user-gil")

	mine := open(t, w, ana, "da Ana", "x")
	hers := open(t, w, bia, "da Bia", "x")
	theirs := open(t, w, gil, "do Gil", "x")

	page, err := w.tickets.List(ctx, ana, ticket.ListQuery{})
	if err != nil || page.Total != 1 || page.Rows[0].ID != mine.ID {
		t.Fatalf("Ana lists %d tickets (%+v), %v; want only hers", page.Total, page.Rows, err)
	}

	// A ticket of the same space that is somebody else's, and a ticket of
	// another space, are answered the same way: as not found.
	for _, other := range []*ticket.Ticket{hers, theirs} {
		if _, err := w.tickets.Find(ctx, ana, other.ID); !errors.Is(err, ticket.ErrNotFound) {
			t.Fatalf("Ana read %s: %v, want ErrNotFound", other.Subject, err)
		}
		if _, err := w.tickets.Reply(ctx, ana, other.ID, ticket.ReplyRequest{Body: "oi"}, nil); !errors.Is(err, ticket.ErrNotFound) {
			t.Fatalf("Ana replied to %s: %v, want ErrNotFound", other.Subject, err)
		}
		if _, err := w.tickets.Close(ctx, ana, other.ID); !errors.Is(err, ticket.ErrNotFound) {
			t.Fatalf("Ana closed %s: %v, want ErrNotFound", other.Subject, err)
		}
		if _, err := w.tickets.Reopen(ctx, ana, other.ID); !errors.Is(err, ticket.ErrNotFound) {
			t.Fatalf("Ana reopened %s: %v, want ErrNotFound", other.Subject, err)
		}
		if _, _, err := w.tickets.Attachment(ctx, ana, other.ID, "any"); !errors.Is(err, ticket.ErrNotFound) {
			t.Fatalf("Ana opened a file of %s: %v, want ErrNotFound", other.Subject, err)
		}
	}
	if _, err := w.tickets.Find(ctx, ana, "no-such-ticket"); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("a ticket that does not exist answered %v", err)
	}

	// Somebody who may read the whole space reads Bia's, and still not Gil's.
	boss := owner("acme", "user-boss")
	page, err = w.tickets.List(ctx, boss, ticket.ListQuery{})
	if err != nil || page.Total != 2 {
		t.Fatalf("the owner lists %d tickets, %v; want the two of the space", page.Total, err)
	}
	if _, err := w.tickets.Find(ctx, boss, hers.ID); err != nil {
		t.Fatalf("the owner reading a member's ticket: %v", err)
	}
	if _, err := w.tickets.Find(ctx, boss, theirs.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("the owner read another space's ticket: %v", err)
	}
	if _, err := w.tickets.Reply(ctx, boss, hers.ID, ticket.ReplyRequest{Body: "Já estamos vendo."}, nil); err != nil {
		t.Fatalf("the owner replying in the space: %v", err)
	}
}

func TestWhatIsNotGrantedIsRefused(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()

	// A role is not an action: an administrator with no actions can do nothing.
	nobody := security.Subject{ID: "user-x", Tenant: "acme", Roles: []string{"admin", "owner"}, Verified: true}
	if _, err := w.tickets.Open(ctx, nobody, ticket.OpenRequest{Subject: "a", Body: "b"}, nil); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a role opened a ticket: %v", err)
	}
	if _, err := w.tickets.List(ctx, nobody, ticket.ListQuery{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a role listed tickets: %v", err)
	}
	if err := w.tickets.CanOpen(ctx, nobody); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a role was told it may open: %v", err)
	}

	// Reading is not replying: a customer who may only look cannot write.
	ana := member("acme", "user-ana")
	record := open(t, w, ana, "olhar", "x")
	lookOnly := security.Subject{ID: "user-ana", Tenant: "acme", Actions: []security.Action{ticket.TicketViewOwn}}
	if _, err := w.tickets.Find(ctx, lookOnly, record.ID); err != nil {
		t.Fatalf("reading with the right to read: %v", err)
	}
	if _, err := w.tickets.Reply(ctx, lookOnly, record.ID, ticket.ReplyRequest{Body: "x"}, nil); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("replying without the right to: %v", err)
	}
	if _, err := w.tickets.Close(ctx, lookOnly, record.ID); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("closing without the right to: %v", err)
	}

	// A guest, and the zero subject, are nobody's.
	if _, err := w.tickets.List(ctx, security.Guest("acme"), ticket.ListQuery{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a guest listed tickets: %v", err)
	}
	if _, err := w.tickets.List(ctx, security.Subject{}, ticket.ListQuery{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("the zero subject listed tickets: %v", err)
	}
}

func TestARepliesMoveTheTicketAndAClosedOneTakesNone(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana, helper := member("acme", "user-ana"), agent("team-1")
	record := open(t, w, ana, "fluxo", "primeira")

	// The customer writing again before anybody answered leaves it open.
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "mais um detalhe"}, nil); err != nil {
		t.Fatalf("replying: %v", err)
	}
	if got := statusOf(t, w, ana, record.ID); got != ticket.StatusOpen {
		t.Fatalf("a ticket nobody answered is %s, want open", got)
	}

	// The team answering makes it wait for the customer; the customer
	// answering makes it wait for the team.
	if _, err := w.team.Reply(ctx, helper, record.ID, ticket.ReplyRequest{Body: "Pode reiniciar?"}, nil); err != nil {
		t.Fatalf("the team replying: %v", err)
	}
	if got := statusOf(t, w, ana, record.ID); got != ticket.StatusWaitingCustomer {
		t.Fatalf("after the team answered the ticket is %s", got)
	}
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "Reiniciei."}, nil); err != nil {
		t.Fatalf("replying: %v", err)
	}
	if got := statusOf(t, w, ana, record.ID); got != ticket.StatusWaitingTeam {
		t.Fatalf("after the customer answered the ticket is %s", got)
	}

	// Closing and reopening.
	closed, err := w.tickets.Close(ctx, ana, record.ID)
	if err != nil || closed.Status != ticket.StatusClosed || closed.ClosedAt == nil {
		t.Fatalf("closing: %+v, %v", closed, err)
	}
	if _, err := w.tickets.Close(ctx, ana, record.ID); err == nil {
		t.Fatal("a closed ticket was closed again")
	}
	var errs validation.Errors
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "e agora?"}, nil); !errors.As(err, &errs) || len(errs["body"]) == 0 {
		t.Fatalf("a closed ticket took a reply: %v", err)
	}
	if _, err := w.team.Reply(ctx, helper, record.ID, ticket.ReplyRequest{Body: "e agora?"}, nil); !errors.As(err, &errs) {
		t.Fatalf("a closed ticket took an answer from the team: %v", err)
	}
	reopened, err := w.tickets.Reopen(ctx, ana, record.ID)
	if err != nil || reopened.Status != ticket.StatusWaitingTeam || reopened.ClosedAt != nil {
		t.Fatalf("reopening: %+v, %v", reopened, err)
	}
	if _, err := w.tickets.Reopen(ctx, ana, record.ID); err == nil {
		t.Fatal("an open ticket was reopened")
	}

	// A resolved ticket the customer writes on is back with the team.
	if _, err := w.team.SetStatus(ctx, helper, record.ID, ticket.StatusResolved); err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "Voltou."}, nil); err != nil {
		t.Fatalf("replying to a resolved ticket: %v", err)
	}
	if got := statusOf(t, w, ana, record.ID); got != ticket.StatusWaitingTeam {
		t.Fatalf("a resolved ticket the customer wrote on is %s", got)
	}

	got := strings.Join(events(t, w, "acme"), ",")
	want := "ticket.opened,ticket.replied,ticket.replied,ticket.replied,ticket.closed,ticket.reopened,ticket.status_changed,ticket.replied"
	if got != want {
		t.Fatalf("the log of the space holds\n%s\nwant\n%s", got, want)
	}
}

func statusOf(t *testing.T, w world, who security.Subject, id string) ticket.Status {
	t.Helper()
	conv, err := w.tickets.Find(context.Background(), who, id)
	if err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	return conv.Ticket.Status
}

func TestTheListIsPagedSearchedFilteredAndOrderedOnTheServer(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana := member("acme", "user-ana")

	first := open(t, w, ana, "Câmera offline", "x")
	second := open(t, w, ana, "Fatura em aberto", "x")
	third := open(t, w, ana, "Placa não lida", "x")
	if _, err := w.tickets.Close(ctx, ana, second.ID); err != nil {
		t.Fatalf("closing: %v", err)
	}

	page, err := w.tickets.List(ctx, ana, ticket.ListQuery{PerPage: 2, Page: 1, Sort: "number", Dir: "asc"})
	if err != nil || page.Total != 3 || page.Pages != 2 || len(page.Rows) != 2 || page.Rows[0].ID != first.ID || page.Rows[1].ID != second.ID {
		t.Fatalf("page one by number is %d of %d (%d pages), %v", len(page.Rows), page.Total, page.Pages, err)
	}
	page, err = w.tickets.List(ctx, ana, ticket.ListQuery{PerPage: 2, Page: 2, Sort: "number", Dir: "asc"})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ID != third.ID {
		t.Fatalf("page two by number is %+v, %v", page.Rows, err)
	}

	// The newest change first, until somebody orders otherwise: the reply
	// moves a ticket up.
	if _, err := w.tickets.Reply(ctx, ana, first.ID, ticket.ReplyRequest{Body: "de novo"}, nil); err != nil {
		t.Fatalf("replying: %v", err)
	}
	page, _ = w.tickets.List(ctx, ana, ticket.ListQuery{})
	if page.Rows[0].ID != first.ID {
		t.Fatalf("the ticket that changed last is not first: %+v", page.Rows)
	}

	for term, want := range map[string]string{"câmera": first.ID, "FATURA": second.ID, "#000003": third.ID, "3": third.ID, "000002": second.ID} {
		page, err = w.tickets.List(ctx, ana, ticket.ListQuery{Search: term})
		if err != nil || page.Total != 1 || page.Rows[0].ID != want {
			t.Fatalf("searching %q found %d (%+v), %v", term, page.Total, page.Rows, err)
		}
	}
	page, err = w.tickets.List(ctx, ana, ticket.ListQuery{Statuses: []ticket.Status{ticket.StatusClosed}})
	if err != nil || page.Total != 1 || page.Rows[0].ID != second.ID {
		t.Fatalf("filtering the closed found %d, %v", page.Total, err)
	}
	page, err = w.tickets.List(ctx, ana, ticket.ListQuery{Statuses: []ticket.Status{ticket.StatusOpen, ticket.StatusClosed}})
	if err != nil || page.Total != 3 {
		t.Fatalf("filtering two statuses found %d, %v", page.Total, err)
	}

	var errs validation.Errors
	if _, err := w.tickets.List(ctx, ana, ticket.ListQuery{Sort: "tenant_id"}); !errors.As(err, &errs) {
		t.Fatalf("ordering by a column outside the allowlist answered %v", err)
	}
	if _, err := w.tickets.List(ctx, ana, ticket.ListQuery{Statuses: []ticket.Status{"bogus"}}); !errors.As(err, &errs) {
		t.Fatalf("filtering by a status that is not one answered %v", err)
	}
	// The search term is bound, never written into the statement.
	page, err = w.tickets.List(ctx, ana, ticket.ListQuery{Search: "x'; DROP TABLE tickets; --"})
	if err != nil || page.Total != 0 {
		t.Fatalf("a hostile term found %d, %v", page.Total, err)
	}
	if page, err = w.tickets.List(ctx, ana, ticket.ListQuery{}); err != nil || page.Total != 3 {
		t.Fatalf("the table after the hostile term holds %d tickets, %v", page.Total, err)
	}
}
