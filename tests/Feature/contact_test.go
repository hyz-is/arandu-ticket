package feature_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/validation"

	ticket "github.com/hyz-is/arandu-ticket"
)

func withAccounts(c *ticket.Config) {
	c.Accounts = accounts{
		"ana@acme.test":    {UserID: "user-ana", Tenant: "acme", Name: "Ana Souza", Email: "ana@acme.test"},
		"gil@globex.test":  {UserID: "user-gil", Tenant: "globex", Name: "Gil Lopes", Email: "gil@globex.test"},
		"bad@tenant.test":  {UserID: "user-bad", Tenant: "Not/A Tenant", Email: "bad@tenant.test"},
		"nouser@acme.test": {Tenant: "acme", Email: "nouser@acme.test"},
	}
}

func TestTheContactFormOpensATicketOnlyForAnAccount(t *testing.T) {
	t.Parallel()
	w := installWith(t, withAccounts)
	ctx := context.Background()

	result, err := w.tickets.OpenFromContact(ctx, ticket.ContactInput{
		Email: "  Ana@Acme.test ", Name: "Ana", Subject: "Preciso de ajuda", Body: "A câmera da portaria caiu.",
	})
	if err != nil || result.Duplicate || result.Ticket == nil {
		t.Fatalf("opening from the form: %+v, %v", result, err)
	}
	opened := result.Ticket
	if opened.TenantID != "acme" || opened.RequesterID != "user-ana" || opened.Origin != ticket.OriginContactForm {
		t.Fatalf("the ticket is in %q for %q from %q; want the account's space, the user, the form", opened.TenantID, opened.RequesterID, opened.Origin)
	}
	if opened.RequesterName != "Ana Souza" || opened.RequesterEmail != "ana@acme.test" || opened.Number != 1 || opened.Status != ticket.StatusOpen {
		t.Fatalf("the ticket carries %q <%q> #%d %s", opened.RequesterName, opened.RequesterEmail, opened.Number, opened.Status)
	}

	// The customer finds it among their own, and the first message is theirs.
	conv, err := w.tickets.Find(ctx, member("acme", "user-ana"), opened.ID)
	if err != nil || len(conv.Messages) != 1 || conv.Messages[0].Body != "A câmera da portaria caiu." || conv.Messages[0].AuthorID != "user-ana" {
		t.Fatalf("the conversation is %+v, %v", conv, err)
	}
	// Nobody else does.
	if _, err := w.tickets.Find(ctx, member("acme", "user-bia"), opened.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("somebody else read it: %v", err)
	}

	// The team is told, and the log names the user as who asked.
	sent := w.notices.all()
	if len(sent) != 1 || sent[0].Kind != ticket.EventOpened || sent[0].Ticket.Origin != ticket.OriginContactForm {
		t.Fatalf("the notifier was told %+v", sent)
	}
	logged := entries(t, w, "acme")
	if len(logged) != 1 || logged[0].Event != "ticket.opened" || logged[0].Causer != "user-ana" {
		t.Fatalf("the log holds %+v", logged)
	}
}

func TestTheContactFormOpensNothingWithoutAnAccount(t *testing.T) {
	t.Parallel()
	w := installWith(t, withAccounts)
	ctx := context.Background()

	result, err := w.tickets.OpenFromContact(ctx, ticket.ContactInput{Email: "stranger@nowhere.test", Subject: "Oi", Body: "Posso?"})
	if !errors.Is(err, ticket.ErrNoAccount) || result.Ticket != nil {
		t.Fatalf("a stranger's form answered %+v, %v; want ErrNoAccount and nothing", result, err)
	}
	if n := countTickets(t, w); n != 0 {
		t.Fatalf("the form left %d tickets behind", n)
	}
	if len(w.notices.all()) != 0 || len(entries(t, w, "acme")) != 0 {
		t.Fatal("a form that opened nothing told somebody, or wrote the log")
	}
}

func TestTheContactFormIsRefusedWhenTheApplicationAnswersWrong(t *testing.T) {
	t.Parallel()
	w := installWith(t, withAccounts)
	ctx := context.Background()

	for _, email := range []string{"bad@tenant.test", "nouser@acme.test"} {
		if _, err := w.tickets.OpenFromContact(ctx, ticket.ContactInput{Email: email, Subject: "x", Body: "y"}); err == nil || errors.Is(err, ticket.ErrNoAccount) {
			t.Fatalf("an account the resolver answered badly (%s) gave %v", email, err)
		}
	}
	if n := countTickets(t, w); n != 0 {
		t.Fatalf("a bad answer left %d tickets behind", n)
	}

	// With no resolver at all, there is no account to open for.
	bare := install(t)
	if _, err := bare.tickets.OpenFromContact(ctx, ticket.ContactInput{Email: "ana@acme.test", Subject: "x", Body: "y"}); err == nil || errors.Is(err, ticket.ErrNoAccount) {
		t.Fatalf("no resolver answered %v, want a wiring error", err)
	}
}

func TestTheContactFormValidatesWhatItIsGiven(t *testing.T) {
	t.Parallel()
	w := installWith(t, withAccounts)
	ctx := context.Background()

	var errs validation.Errors
	_, err := w.tickets.OpenFromContact(ctx, ticket.ContactInput{Email: "not-an-email", Subject: " ", Body: ""})
	if !errors.As(err, &errs) || len(errs["email"]) == 0 || len(errs["subject"]) == 0 || len(errs["body"]) == 0 {
		t.Fatalf("an empty form answered %v", err)
	}
	if n := countTickets(t, w); n != 0 {
		t.Fatalf("a rejected form left %d tickets behind", n)
	}
}

func TestTheSameFormSentTwiceOpensOneTicket(t *testing.T) {
	t.Parallel()
	w := installWith(t, withAccounts)
	ctx := context.Background()
	in := ticket.ContactInput{Email: "ana@acme.test", Subject: "Sem sinal", Body: "Está sem sinal desde ontem."}

	first, err := w.tickets.OpenFromContact(ctx, in)
	if err != nil || first.Duplicate {
		t.Fatalf("the first: %+v, %v", first, err)
	}
	// The same words, spelled with different spacing and case in the address.
	again := ticket.ContactInput{Email: "ANA@acme.test", Subject: "Sem  sinal ", Body: "Está sem sinal\ndesde ontem. "}
	second, err := w.tickets.OpenFromContact(ctx, again)
	if err != nil || !second.Duplicate || second.Ticket.ID != first.Ticket.ID {
		t.Fatalf("the repeat: %+v, %v; want the first ticket, flagged", second, err)
	}
	if n := countTickets(t, w); n != 1 {
		t.Fatalf("the form opened %d tickets", n)
	}
	if got := len(w.notices.all()); got != 1 {
		t.Fatalf("the team was told %d times", got)
	}
	if got := len(entries(t, w, "acme")); got != 1 {
		t.Fatalf("the log holds %d entries", got)
	}

	// Other words, or another account, are another ticket.
	other, err := w.tickets.OpenFromContact(ctx, ticket.ContactInput{Email: "ana@acme.test", Subject: "Sem sinal", Body: "Outro problema."})
	if err != nil || other.Duplicate || other.Ticket.ID == first.Ticket.ID || other.Ticket.Number != 2 {
		t.Fatalf("other words: %+v, %v", other, err)
	}
	theirs, err := w.tickets.OpenFromContact(ctx, ticket.ContactInput{Email: "gil@globex.test", Subject: "Sem sinal", Body: "Está sem sinal desde ontem."})
	if err != nil || theirs.Duplicate || theirs.Ticket.TenantID != "globex" {
		t.Fatalf("another account: %+v, %v", theirs, err)
	}

	// Past the window the same words are a new request.
	w.clock.advance(ticket.DefaultContactWindow + time.Minute)
	later, err := w.tickets.OpenFromContact(ctx, in)
	if err != nil || later.Duplicate || later.Ticket.ID == first.Ticket.ID {
		t.Fatalf("after the window: %+v, %v", later, err)
	}
}

func TestTwoIdenticalFormsAtTheSameInstantOpenOneTicket(t *testing.T) {
	t.Parallel()
	w := installWith(t, withAccounts)
	ctx := context.Background()
	in := ticket.ContactInput{Email: "ana@acme.test", Subject: "Duplo clique", Body: "Cliquei duas vezes."}

	const sends = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		created int
		ids     = map[string]bool{}
	)
	for i := 0; i < sends; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := w.tickets.OpenFromContact(ctx, in)
			if err != nil {
				t.Errorf("sending: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			ids[result.Ticket.ID] = true
			if !result.Duplicate {
				created++
			}
		}()
	}
	wg.Wait()
	if created != 1 || len(ids) != 1 {
		t.Fatalf("%d sends created %d tickets and answered %d different ones; want one", sends, created, len(ids))
	}
	if n := countTickets(t, w); n != 1 {
		t.Fatalf("the table holds %d tickets", n)
	}
}

// countTickets is how many tickets the table holds, in every space.
func countTickets(t *testing.T, w world) int {
	t.Helper()
	var n int
	if err := w.sql.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	return n
}
