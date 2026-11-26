package feature_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/validation"

	ticket "github.com/hyz-is/arandu-ticket"
)

// queue is the tenant the visitors' tickets live in, in these tests.
const queue = "visitors"

func withVisitors(c *ticket.Config) { c.VisitorTenant = queue }

// visitorID is the requester a visitor's tickets carry for an identity.
func visitorID(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return "visitor:" + hex.EncodeToString(sum[:])[:32]
}

// visit sends what a visitor said and fails the test if it is refused.
func visit(t *testing.T, w world, in ticket.VisitorInput) ticket.VisitorResult {
	t.Helper()
	result, err := w.tickets.OpenFromVisitor(context.Background(), in)
	if err != nil {
		t.Fatalf("a visitor sending %q: %v", in.Subject, err)
	}
	return result
}

func TestAVisitorOpensATicketInTheQueueAndTheSameFormTwiceOpensOne(t *testing.T) {
	t.Parallel()
	w := installWith(t, withVisitors)
	in := ticket.VisitorInput{Name: "Bia Lima", Email: "  Bia@Example.test ", Subject: "Quero contratar", Body: "Vocês atendem em Recife?"}

	first := visit(t, w, in)
	opened := first.Ticket
	if first.Duplicate || first.Appended || opened == nil {
		t.Fatalf("the first form answered %+v", first)
	}
	if opened.TenantID != queue || opened.Origin != ticket.OriginContactForm || opened.Number != 1 || opened.Status != ticket.StatusOpen {
		t.Fatalf("the ticket is in %q from %q, #%d %s; want the queue, the contact form, #1, open", opened.TenantID, opened.Origin, opened.Number, opened.Status)
	}
	if opened.RequesterID != visitorID("bia@example.test") || opened.RequesterName != "Bia Lima" || opened.RequesterEmail != "bia@example.test" {
		t.Fatalf("the requester is %q %q <%q>", opened.RequesterID, opened.RequesterName, opened.RequesterEmail)
	}

	// The same words, spelled differently, are the same form.
	again := visit(t, w, ticket.VisitorInput{Email: "BIA@example.test", Subject: "Quero  contratar", Body: "Vocês atendem\nem Recife? "})
	if !again.Duplicate || again.Ticket.ID != opened.ID {
		t.Fatalf("the repeat answered %+v; want the first ticket, flagged", again)
	}
	// The same words on another channel are another request.
	mailed := visit(t, w, ticket.VisitorInput{Email: "bia@example.test", Subject: "Quero contratar", Body: "Vocês atendem em Recife?", Origin: ticket.OriginEmail})
	if mailed.Duplicate || mailed.Ticket.ID == opened.ID || mailed.Ticket.Origin != ticket.OriginEmail {
		t.Fatalf("the same words by e-mail answered %+v", mailed)
	}
	// Past the window the same words are a new request.
	w.clock.advance(ticket.DefaultContactWindow + time.Minute)
	if later := visit(t, w, in); later.Duplicate || later.Ticket.ID == opened.ID {
		t.Fatalf("after the window: %+v", later)
	}

	// The team is told, as of any ticket opened, and the ticket says it is a
	// visitor's; the log of the queue says it was opened, with the system as
	// its causer, because a visitor is nobody's user.
	sent := w.notices.all()
	if len(sent) != 3 || sent[0].Kind != ticket.EventOpened || !sent[0].Ticket.Visitor || sent[0].Ticket.RequesterEmail != "bia@example.test" {
		t.Fatalf("the notifier was told %+v", sent)
	}
	logged := entries(t, w, queue)
	if len(logged) != 3 || logged[0].Event != ticket.ActivityTicketOpened || logged[0].Causer != "system" {
		t.Fatalf("the queue's log holds %+v", logged)
	}
}

func TestAThreadTakesTheNextMessageUntilItsTicketIsClosed(t *testing.T) {
	t.Parallel()
	w := installWith(t, withVisitors)
	ctx := context.Background()
	rita := agent("team-1")
	const phone = "+55 81 99876-5432"

	first := visit(t, w, ticket.VisitorInput{Name: "Caio", Origin: ticket.OriginWhatsApp, Thread: phone, Subject: "WhatsApp", Body: "Oi, quero saber o preço."})
	if first.Appended || first.Duplicate || first.Ticket.Origin != ticket.OriginWhatsApp {
		t.Fatalf("the first message answered %+v", first)
	}
	opened := first.Ticket
	if opened.RequesterID != visitorID(phone) || opened.RequesterEmail != "" || opened.RequesterName != "Caio" {
		t.Fatalf("the requester is %q %q <%q>", opened.RequesterID, opened.RequesterName, opened.RequesterEmail)
	}

	// The number is nowhere in the clear: the row keeps digests of it, and
	// nothing else of the conversation.
	var key, requester, everything string
	if err := w.sql.QueryRow(`SELECT origin_key, requester_id, subject || requester_name || requester_email || origin FROM tickets WHERE id = ?`, opened.ID).Scan(&key, &requester, &everything); err != nil {
		t.Fatal(err)
	}
	if !isHex(key) || len(key) != 64 || !isHex(strings.TrimPrefix(requester, "visitor:")) || strings.Contains(everything, "99876") {
		t.Fatalf("the thread is kept as %q and the requester as %q; want their digests", key, requester)
	}

	// The team answers; the next message of the conversation is a reply on
	// the same ticket, and the status follows the customer's rule.
	if _, err := w.team.Reply(ctx, rita, opened.ID, ticket.ReplyRequest{Body: "R$ 99 por mês."}, nil); err != nil {
		t.Fatalf("answering: %v", err)
	}
	next := visit(t, w, ticket.VisitorInput{Name: "Caio", Origin: ticket.OriginWhatsApp, Thread: " +55 81  99876-5432", Subject: "WhatsApp", Body: "Fechado, como pago?"})
	if !next.Appended || next.Duplicate || next.Ticket.ID != opened.ID || next.Ticket.Status != ticket.StatusWaitingTeam {
		t.Fatalf("the next message answered %+v (%s)", next, next.Ticket.Status)
	}
	// The channel sending it twice writes it once.
	twice := visit(t, w, ticket.VisitorInput{Name: "Caio", Origin: ticket.OriginWhatsApp, Thread: phone, Subject: "WhatsApp", Body: "Fechado,  como pago?"})
	if !twice.Duplicate || !twice.Appended || twice.Ticket.ID != opened.ID {
		t.Fatalf("the repeat answered %+v", twice)
	}
	conv, err := w.team.Find(ctx, rita, opened.ID)
	if err != nil || len(conv.Messages) != 3 || conv.Messages[2].Role != ticket.RoleCustomer || conv.Messages[2].AuthorID != visitorID(phone) {
		t.Fatalf("the conversation is %+v, %v", conv.Messages, err)
	}
	if !conv.Ticket.Visitor {
		t.Fatal("the team's conversation does not say the ticket is a visitor's")
	}
	if got := events(t, w, queue); strings.Join(got, ",") != "ticket.opened,ticket.replied,ticket.visitor_replied" {
		t.Fatalf("the queue's log holds %v", got)
	}
	sent := w.notices.all()
	if last := sent[len(sent)-1]; last.Kind != ticket.EventCustomerReplied || !last.Kind.ForTeam() || last.Message.Body != "Fechado, como pago?" {
		t.Fatalf("the notifier was last told %+v", last)
	}

	// The same number on another channel is another conversation.
	other := visit(t, w, ticket.VisitorInput{Email: "caio@example.test", Origin: ticket.OriginEmail, Thread: phone, Subject: "Pagamento", Body: "Mandei o comprovante."})
	if other.Appended || other.Ticket.ID == opened.ID {
		t.Fatalf("another channel's thread answered %+v", other)
	}

	// Once closed, the conversation opens a new ticket.
	if _, err := w.team.SetStatus(ctx, rita, opened.ID, ticket.StatusClosed); err != nil {
		t.Fatalf("closing: %v", err)
	}
	reopened := visit(t, w, ticket.VisitorInput{Name: "Caio", Origin: ticket.OriginWhatsApp, Thread: phone, Subject: "WhatsApp", Body: "Fechado, como pago?"})
	if reopened.Appended || reopened.Duplicate || reopened.Ticket.ID == opened.ID {
		t.Fatalf("after closing, the conversation answered %+v", reopened)
	}
	if again := visit(t, w, ticket.VisitorInput{Name: "Caio", Origin: ticket.OriginWhatsApp, Thread: phone, Subject: "WhatsApp", Body: "Alô?"}); !again.Appended || again.Ticket.ID != reopened.Ticket.ID {
		t.Fatalf("the new ticket did not take the next message: %+v", again)
	}
}

func TestMessagesOfOneThreadAtTheSameInstantOpenOneTicket(t *testing.T) {
	t.Parallel()
	w := installWith(t, withVisitors)

	const sends = 6
	var wg sync.WaitGroup
	for i := 0; i < sends; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := w.tickets.OpenFromVisitor(context.Background(), ticket.VisitorInput{
				Name: "Dani", Origin: ticket.OriginWhatsApp, Thread: "+5511900001111", Subject: "WhatsApp", Body: "mensagem " + string(rune('a'+i)),
			}); err != nil {
				t.Errorf("sending: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if n := countTickets(t, w); n != 1 {
		t.Fatalf("one conversation opened %d tickets", n)
	}
	var messages int
	if err := w.sql.QueryRow(`SELECT COUNT(*) FROM ticket_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != sends {
		t.Fatalf("the ticket holds %d messages, want %d", messages, sends)
	}
}

func TestVisitorsAreOffWithoutAQueue(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()

	if _, err := w.tickets.OpenFromVisitor(ctx, ticket.VisitorInput{Email: "bia@example.test", Subject: "x", Body: "y"}); !errors.Is(err, ticket.ErrVisitorsOff) {
		t.Fatalf("without a queue OpenFromVisitor answered %v", err)
	}
	if _, err := w.tickets.AdoptVisitorTickets(ctx, ticket.Account{UserID: "u", Tenant: "acme", Email: "bia@example.test"}); !errors.Is(err, ticket.ErrVisitorsOff) {
		t.Fatalf("without a queue AdoptVisitorTickets answered %v", err)
	}
	if n := countTickets(t, w); n != 0 || w.module.URLs().TeamVisitors() != "" {
		t.Fatalf("visitors off left %d tickets, or a queue's address %q", n, w.module.URLs().TeamVisitors())
	}
}

func TestWhatAVisitorSendsIsValidatedByItsOrigin(t *testing.T) {
	t.Parallel()
	w := installWith(t, withVisitors)
	ctx := context.Background()

	for name, c := range map[string]struct {
		in     ticket.VisitorInput
		fields []string
	}{
		"the form with no e-mail":          {ticket.VisitorInput{Subject: "x", Body: "y"}, []string{"email"}},
		"an e-mail with no address":        {ticket.VisitorInput{Origin: ticket.OriginEmail, Subject: "x", Body: "y"}, []string{"email"}},
		"an address with a name in it":     {ticket.VisitorInput{Email: "Bia <bia@example.test>", Subject: "x", Body: "y"}, []string{"email"}},
		"the screens' origin":              {ticket.VisitorInput{Origin: ticket.OriginWeb, Email: "bia@example.test", Subject: "x", Body: "y"}, []string{"origin"}},
		"an origin that is none":           {ticket.VisitorInput{Origin: "fax", Email: "bia@example.test", Subject: "x", Body: "y"}, []string{"origin"}},
		"WhatsApp with nobody":             {ticket.VisitorInput{Origin: ticket.OriginWhatsApp, Thread: "+5511", Subject: "x", Body: "y"}, []string{"name"}},
		"WhatsApp with no thread":          {ticket.VisitorInput{Origin: ticket.OriginWhatsApp, Name: "Edu", Subject: "x", Body: "y"}, []string{"thread"}},
		"WhatsApp with a broken address":   {ticket.VisitorInput{Origin: ticket.OriginWhatsApp, Name: "Edu", Email: "edu@", Thread: "1", Subject: "x", Body: "y"}, []string{"email"}},
		"no subject and no body":           {ticket.VisitorInput{Email: "bia@example.test"}, []string{"subject", "body"}},
		"a thread longer than it can be":   {ticket.VisitorInput{Email: "bia@example.test", Thread: strings.Repeat("9", 300), Subject: "x", Body: "y"}, []string{"thread"}},
		"a subject longer than it can be":  {ticket.VisitorInput{Email: "bia@example.test", Subject: strings.Repeat("a", 200), Body: "y"}, []string{"subject"}},
		"a body longer than it can be":     {ticket.VisitorInput{Email: "bia@example.test", Subject: "x", Body: strings.Repeat("a", ticket.MaxBodyChars+1)}, []string{"body"}},
		"WhatsApp with nothing to name by": {ticket.VisitorInput{Origin: ticket.OriginWhatsApp, Subject: "x", Body: "y"}, []string{"name", "thread"}},
	} {
		var errs validation.Errors
		if _, err := w.tickets.OpenFromVisitor(ctx, c.in); !errors.As(err, &errs) {
			t.Errorf("%s answered %v, want the input refused", name, err)
			continue
		}
		for _, field := range c.fields {
			if len(errs[field]) == 0 {
				t.Errorf("%s was refused without a message on %s: %v", name, field, errs)
			}
		}
	}
	if n := countTickets(t, w); n != 0 {
		t.Fatalf("a refused input left %d tickets", n)
	}

	// WhatsApp takes a name and a thread, or an e-mail alone.
	if r := visit(t, w, ticket.VisitorInput{Origin: ticket.OriginWhatsApp, Name: "Edu", Thread: "+5511", Subject: "x", Body: "y"}); r.Ticket == nil {
		t.Fatal("WhatsApp with a name and a thread was refused")
	}
	if r := visit(t, w, ticket.VisitorInput{Origin: ticket.OriginWhatsApp, Email: "edu@example.test", Subject: "x", Body: "z"}); r.Ticket.RequesterID != visitorID("edu@example.test") {
		t.Fatalf("WhatsApp with an e-mail alone named the requester %q", r.Ticket.RequesterID)
	}
}

func TestNoCustomerReadsTheQueueAndTheTeamAnswersIt(t *testing.T) {
	t.Parallel()
	w := installWith(t, withVisitors)
	ctx := context.Background()
	rita := agent("team-1")
	boss := owner("acme", "user-boss")
	open(t, w, member("acme", "user-ana"), "da Acme", "x")

	visitor := visit(t, w, ticket.VisitorInput{Name: "Bia", Email: "bia@example.test", Subject: "Orçamento", Body: "Quanto custa?"}).Ticket

	// A customer of a space, even one who reads the whole space, does not find
	// it by its identifier, does not see it listed, and cannot answer it.
	if _, err := w.tickets.Find(ctx, boss, visitor.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("a customer of acme read a visitor's ticket: %v", err)
	}
	page, err := w.tickets.List(ctx, boss, ticket.ListQuery{})
	if err != nil || page.Total != 1 || page.Rows[0].Visitor {
		t.Fatalf("a customer of acme lists %+v, %v", page, err)
	}
	if _, err := w.tickets.Reply(ctx, boss, visitor.ID, ticket.ReplyRequest{Body: "eu respondo"}, nil); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("a customer of acme answered a visitor's ticket: %v", err)
	}
	if _, err := w.tickets.Close(ctx, boss, visitor.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("a customer of acme closed a visitor's ticket: %v", err)
	}

	// The team lists it, narrows its list to the queue, reads and answers it.
	all, err := w.team.List(ctx, rita, ticket.TeamQuery{})
	if err != nil || all.Total != 2 {
		t.Fatalf("the team lists %+v, %v", all, err)
	}
	queued, err := w.team.List(ctx, rita, ticket.TeamQuery{Tenant: queue})
	if err != nil || queued.Total != 1 || queued.Rows[0].ID != visitor.ID || !queued.Rows[0].Visitor || queued.Rows[0].Tenant != queue {
		t.Fatalf("the team's queue is %+v, %v", queued, err)
	}
	if _, err := w.team.Reply(ctx, rita, visitor.ID, ticket.ReplyRequest{Body: "R$ 99 por mês."}, nil); err != nil {
		t.Fatalf("answering a visitor: %v", err)
	}
	sent := w.notices.all()
	last := sent[len(sent)-1]
	if last.Kind != ticket.EventTeamReplied || last.Kind.ForTeam() || last.Ticket.RequesterEmail != "bia@example.test" || !last.Ticket.Visitor || last.Ticket.Status != ticket.StatusWaitingCustomer {
		t.Fatalf("the notifier was told %+v; want the team's answer, for the visitor's address", last)
	}
	if got := events(t, w, queue); strings.Join(got, ",") != "ticket.opened,ticket.replied" {
		t.Fatalf("the queue's log holds %v", got)
	}
}

func TestTheTeamSeesTheQueueOverHTTP(t *testing.T) {
	t.Parallel()
	s := serve(t, installWith(t, withVisitors))
	team := s.signIn(t, agent("team-1"))
	visitor := visit(t, s.world, ticket.VisitorInput{Email: "bia@example.test", Subject: "Orçamento", Body: "Quanto custa?"}).Ticket

	if got := s.module.URLs().TeamVisitors(); got != "/admin/tickets?space="+queue {
		t.Fatalf("the queue's address is %q", got)
	}
	rec := s.answer(t, request{method: http.MethodGet, target: s.module.URLs().TeamVisitors(), cookies: team, json: true})
	list := decode(t, rec)
	if rec.Code != http.StatusOK || list["total"].(float64) != 1 {
		t.Fatalf("the queue answered %d: %s", rec.Code, rec.Body)
	}
	row := list["items"].([]any)[0].(map[string]any)
	if row["visitor"] != true || row["tenant"] != queue || row["origin"] != "contact_form" {
		t.Fatalf("the queue's row is %v", row)
	}
	rec = s.answer(t, request{method: http.MethodPost, target: "/admin/tickets/" + visitor.ID + "/priority", cookies: team, json: true, form: url.Values{"priority": {"high"}}})
	if rec.Code != http.StatusOK || decode(t, rec)["visitor"] != true {
		t.Fatalf("changing a visitor's priority answered %d: %s", rec.Code, rec.Body)
	}
}

func TestAnAccountAdoptsTheVisitorTicketsOfItsEmail(t *testing.T) {
	t.Parallel()
	w := installWith(t, withVisitors)
	ctx := context.Background()
	rita := agent("team-1")
	ana := member("acme", "user-ana")

	// The account's space already has a ticket, so what moves takes the next
	// numbers.
	open(t, w, ana, "da conta", "x")
	formed := visit(t, w, ticket.VisitorInput{Name: "Ana", Email: "ana@acme.test", Subject: "Antes da conta", Body: "Posso testar?"}).Ticket
	chatted := visit(t, w, ticket.VisitorInput{Name: "Ana", Email: "ANA@acme.test", Origin: ticket.OriginWhatsApp, Thread: "+5581", Subject: "WhatsApp", Body: "Oi"}).Ticket
	filed := visit(t, w, ticket.VisitorInput{Email: "ana@acme.test", Subject: "Com anexo", Body: "x"}).Ticket
	theirs := visit(t, w, ticket.VisitorInput{Email: "bia@example.test", Subject: "De outra pessoa", Body: "x"}).Ticket

	// The team answers one, writes a note on it and closes it; the closed one
	// moves too. The team's answer on another carries a file, which keeps that
	// ticket in the queue.
	if _, err := w.team.Reply(ctx, rita, formed.ID, ticket.ReplyRequest{Body: "Pode sim."}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.team.Note(ctx, rita, formed.ID, ticket.ReplyRequest{Body: "lead quente"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.team.SetStatus(ctx, rita, formed.ID, ticket.StatusClosed); err != nil {
		t.Fatal(err)
	}
	if _, err := w.team.Reply(ctx, rita, filed.ID, ticket.ReplyRequest{Body: "segue"}, []ticket.Upload{upload("tabela.png", png)}); err != nil {
		t.Fatal(err)
	}

	account := ticket.Account{UserID: "user-ana", Tenant: "acme", Name: "Ana Souza", Email: "Ana@Acme.test"}
	moved, err := w.tickets.AdoptVisitorTickets(ctx, account)
	if err != nil {
		t.Fatalf("adopting: %v", err)
	}
	if len(moved) != 2 || moved[0].ID != formed.ID || moved[1].ID != chatted.ID {
		t.Fatalf("adopted %+v; want the form's and the conversation's, oldest first", moved)
	}
	for i, s := range moved {
		if s.Tenant != "acme" || s.Number != int64(i+2) || s.Visitor || s.RequesterID != "user-ana" || s.RequesterName != "Ana Souza" || s.RequesterEmail != "Ana@Acme.test" {
			t.Errorf("an adopted ticket answers %+v", s)
		}
	}
	if moved[0].Status != ticket.StatusClosed {
		t.Errorf("the adopted ticket left its status behind: %s", moved[0].Status)
	}

	// The customer reads them now, with what the visitor wrote as theirs, the
	// team's answer, no note, and the line that says what happened.
	conv, err := w.tickets.Find(ctx, ana, formed.ID)
	if err != nil {
		t.Fatalf("the account reads the adopted ticket: %v", err)
	}
	if len(conv.Messages) != 3 || conv.Messages[0].AuthorID != "user-ana" || conv.Messages[0].Body != "Posso testar?" ||
		conv.Messages[1].Role != ticket.RoleTeam || conv.Messages[2].Role != ticket.RoleSystem || conv.Messages[2].Body != "Este contato foi ligado à sua conta." {
		t.Fatalf("the adopted conversation is %+v", conv.Messages)
	}
	if page, err := w.tickets.List(ctx, ana, ticket.ListQuery{}); err != nil || page.Total != 3 {
		t.Fatalf("the account lists %d tickets, %v; want its own and the two adopted", page.Total, err)
	}
	// The team still has every message, the note included, under the account's
	// space, and nothing of the moved tickets is left in the queue.
	teamConv, err := w.team.Find(ctx, rita, formed.ID)
	if err != nil || len(teamConv.Messages) != 4 || teamConv.Ticket.Tenant != "acme" || teamConv.Ticket.Visitor {
		t.Fatalf("the team reads %+v, %v", teamConv, err)
	}
	var left int
	if err := w.sql.QueryRow(`SELECT COUNT(*) FROM ticket_messages WHERE tenant_id = ? AND ticket_id IN (?, ?)`, queue, formed.ID, chatted.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d messages of the moved tickets stayed in the queue (%v)", left, err)
	}

	// What carried a file, and what is somebody else's, stayed.
	queued, err := w.team.List(ctx, rita, ticket.TeamQuery{Tenant: queue})
	if err != nil || queued.Total != 2 || !sameSet([]string{queued.Rows[0].ID, queued.Rows[1].ID}, []string{filed.ID, theirs.ID}) {
		t.Fatalf("the queue holds %+v, %v", queued.Rows, err)
	}
	if logged := w.logs.String(); !strings.Contains(logged, "skipped=1") || strings.Contains(logged, "ana@acme.test") {
		t.Fatalf("the process log holds %q; want the skipped ticket counted and no address", logged)
	}

	// The account's log says each ticket was linked, by the account.
	var adopted []entry
	for _, e := range entries(t, w, "acme") {
		if e.Event == ticket.ActivityTicketAdopted {
			adopted = append(adopted, e)
		}
	}
	if len(adopted) != 2 || adopted[0].Causer != "user-ana" || adopted[0].Subject != formed.ID {
		t.Fatalf("the account's log holds %+v", adopted)
	}

	// Calling it again finds nothing.
	again, err := w.tickets.AdoptVisitorTickets(ctx, account)
	if err != nil || len(again) != 0 {
		t.Fatalf("adopting again moved %+v, %v", again, err)
	}
}

func TestTwoAdoptionsAtTheSameInstantMoveEachTicketOnce(t *testing.T) {
	t.Parallel()
	w := installWith(t, withVisitors)
	ctx := context.Background()

	const tickets = 4
	for i := 0; i < tickets; i++ {
		visit(t, w, ticket.VisitorInput{Email: "gil@globex.test", Subject: "pedido", Body: "número " + string(rune('a'+i))})
	}
	account := ticket.Account{UserID: "user-gil", Tenant: "globex", Email: "gil@globex.test"}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			moved, err := w.tickets.AdoptVisitorTickets(ctx, account)
			if err != nil {
				t.Errorf("adopting: %v", err)
				return
			}
			mu.Lock()
			total += len(moved)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != tickets {
		t.Fatalf("two adoptions moved %d tickets between them, want %d", total, tickets)
	}
	var inSpace, distinct, inQueue int
	if err := w.sql.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT number) FROM tickets WHERE tenant_id = 'globex'`).Scan(&inSpace, &distinct); err != nil {
		t.Fatal(err)
	}
	if err := w.sql.QueryRow(`SELECT COUNT(*) FROM tickets WHERE tenant_id = ?`, queue).Scan(&inQueue); err != nil {
		t.Fatal(err)
	}
	if inSpace != tickets || distinct != tickets || inQueue != 0 {
		t.Fatalf("the space holds %d tickets with %d numbers and the queue %d; want %d, %d and none", inSpace, distinct, inQueue, tickets, tickets)
	}
}

func TestACustomersChannelNamesTheOriginAndTheScreensNeverDo(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana := member("acme", "user-ana")

	record, err := w.tickets.Open(ctx, ana, ticket.OpenRequest{Subject: "pelo WhatsApp", Body: "x", Origin: ticket.OriginWhatsApp}, nil)
	if err != nil || record.Origin != ticket.OriginWhatsApp || record.RequesterID != "user-ana" || record.TenantID != "acme" {
		t.Fatalf("opening from WhatsApp as the customer: %+v, %v", record, err)
	}
	if mailed, err := w.tickets.Open(ctx, ana, ticket.OpenRequest{Subject: "por e-mail", Body: "x", Origin: ticket.OriginEmail}, nil); err != nil || mailed.Origin != ticket.OriginEmail {
		t.Fatalf("opening from an e-mail as the customer: %+v, %v", mailed, err)
	}
	if plain := open(t, w, ana, "pela tela", "x"); plain.Origin != ticket.OriginWeb {
		t.Fatalf("an empty origin is %q, want web", plain.Origin)
	}
	for _, origin := range []ticket.Origin{ticket.OriginContactForm, "fax"} {
		var errs validation.Errors
		if _, err := w.tickets.Open(ctx, ana, ticket.OpenRequest{Subject: "x", Body: "y", Origin: origin}, nil); !errors.As(err, &errs) || len(errs["origin"]) == 0 {
			t.Errorf("the origin %q answered %v, want it refused", origin, err)
		}
	}

	// The package's own form opens on the screens, whatever the form says.
	s := serve(t, w)
	cookies := s.signIn(t, ana)
	rec := s.answer(t, request{method: http.MethodPost, target: "/tickets", cookies: cookies, json: true,
		form: url.Values{"subject": {"pelo site"}, "body": {"x"}, "origin": {"whatsapp"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("opening over HTTP answered %d: %s", rec.Code, rec.Body)
	}
	var origin string
	if err := w.sql.QueryRow(`SELECT origin FROM tickets WHERE id = ?`, decode(t, rec)["id"]).Scan(&origin); err != nil || origin != string(ticket.OriginWeb) {
		t.Fatalf("a ticket opened on the screens has the origin %q (%v)", origin, err)
	}
}

// isHex says the text is hexadecimal digits only.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
