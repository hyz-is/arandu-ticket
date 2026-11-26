package ticket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/database"
)

// visitorPrefix starts the requester of every visitor's ticket, so the
// identifier can never be mistaken for a user of the application.
const visitorPrefix = "visitor:"

// OpenFromVisitor opens a ticket for somebody who has no account, in the
// visitors' queue (Config.VisitorTenant), or adds their message to the open
// ticket of the same conversation.
//
// It is for the application's public contact form when the e-mail belongs to
// no account (OpenFromContact answered ErrNoAccount), for an e-mail the
// application received, and for a WhatsApp conversation with somebody the
// application does not know. The origin is VisitorInput.Origin, the contact form
// when empty.
//
// The ticket opens in the visitors' queue, with the subject and the body as its
// first message. Its requester is "visitor:" and the first 32 hex digits of the
// SHA-256 of the e-mail in lower case -- or of the Thread, when there is no
// e-mail -- with the name and the e-mail the visitor gave. No customer reads
// the queue: no subject is of that tenant, so the customer's policy refuses
// every ticket in it, and the team reads and answers it as any other space's.
// The team's answer is told to the Notifier as EventTeamReplied, with the
// visitor's e-mail as the requester's, for the application to send.
//
// The same e-mail, subject and body of the same origin inside
// Config.ContactWindow open one ticket: the second answer is the first ticket,
// with Duplicate set. With a Thread, a ticket of the same origin and thread
// that is not closed takes the message as a reply from the customer instead of
// a new ticket being opened (Appended), and the same words as its last message
// inside the window are a repeat (Duplicate). A thread whose ticket was closed
// opens a new one.
//
// There is no session behind any of these, so the Grant is the system's,
// scoped to Config.VisitorTenant, and the sender is who the channel says they
// are: put a captcha or a rate limit in front of a form. Without
// Config.VisitorTenant the answer is ErrVisitorsOff and nothing is read.
func (s *TicketService) OpenFromVisitor(ctx context.Context, in VisitorInput) (VisitorResult, error) {
	if s.cfg.VisitorTenant == "" {
		return VisitorResult{}, ErrVisitorsOff
	}
	origin := in.Origin
	if origin == "" {
		origin = OriginContactForm
	}
	email := strings.ToLower(trimmed(in.Email))
	name := clip(trimmed(in.Name), maxNameLen)
	thread := squash(in.Thread)
	subject, body := trimmed(in.Subject), trimmed(in.Body)

	errs := validation.Errors{}
	switch origin {
	case OriginContactForm, OriginEmail, OriginWhatsApp:
	default:
		errs.Add("origin", s.labels.message("origin"))
	}
	// An e-mail is required on the form and on a received e-mail, and checked
	// whenever one was given: it names the requester and finds the account
	// later.
	if email != "" || origin != OriginWhatsApp {
		if !address(email) {
			errs.Add("email", s.labels.message("email"))
		}
	}
	// On WhatsApp the visitor is known by a name or an e-mail, and the
	// conversation by its thread when there is no e-mail to name them by.
	if origin == OriginWhatsApp && email == "" {
		if name == "" {
			errs.Add("name", s.labels.message("required"))
		}
		if thread == "" {
			errs.Add("thread", s.labels.message("required"))
		}
	}
	if utf8.RuneCountInString(thread) > maxKeyLen {
		errs.Add("thread", s.labels.message("max", map[string]any{"max": maxKeyLen}))
	}
	s.validateWords(errs, subject, body)
	if errs.Any() {
		return VisitorResult{}, errs
	}

	g, err := visitorGrant(s.cfg.VisitorTenant)
	if err != nil {
		return VisitorResult{}, err
	}
	identity := email
	if identity == "" {
		identity = thread
	}
	d := draft{Subject: subject, Body: body, Origin: origin, Requester: Person{ID: visitorRequester(identity), Name: name, Email: email}}
	if thread != "" {
		d.OriginKey, d.Thread = threadKey(origin, thread), true
	} else {
		d.OriginKey = visitorKey(origin, email, subject, body)
	}
	got, err := s.receive(ctx, g, d)
	if err != nil {
		return VisitorResult{}, err
	}
	if got.duplicate {
		return VisitorResult{Ticket: got.ticket, Duplicate: true, Appended: got.appended}, nil
	}

	// A visitor is nobody's user, so the entry names no user as its causer and
	// the log attributes it to the system.
	event, kind := ActivityTicketOpened, EventOpened
	if got.appended {
		event, kind = ActivityTicketVisitorReplied, EventCustomerReplied
	}
	if err := s.record(ctx, g, security.Subject{}, event, got.ticket, map[string]any{"origin": string(origin), "visitor": true}); err != nil {
		return VisitorResult{}, err
	}
	s.notify(ctx, kind, summaryOf(got.ticket), messageViewOf(got.message, nil))
	return VisitorResult{Ticket: got.ticket, Appended: got.appended}, nil
}

// AdoptVisitorTickets moves into the account's space every ticket of the
// visitors' queue whose requester's e-mail is the account's, and answers them
// as they are there: with the account's tenant and a new number.
//
// # The application calls it only with an e-mail it has proven
//
// Call it after the person confirmed that they own Account.Email -- the link of
// the confirmation e-mail was opened -- and never with an address somebody only
// typed. Anybody can write anybody's address on a contact form, and the
// tickets this moves carry what the real owner of the address wrote: handing
// them to an account that did not prove the address hands them to whoever
// typed it first.
//
// Every ticket of the queue with that e-mail is moved, whatever its status,
// and the comparison ignores case. A ticket that carries a file is left where
// it is and counted in the log (Config.Logger): its bytes are kept under the
// queue's own prefix on the Disk, and moving them is not something a database
// transaction can undo, so it is never moved halfway. Everything else moves in
// one transaction: the ticket takes the next number of the account's space,
// its messages follow it, its requester becomes the account, and a line the
// system writes, which the customer reads, says the contact was linked to the
// account. The team's triage (priority, assignee, status) is kept. Each move is
// recorded as ActivityTicketAdopted in the account's space, with the account as
// its causer.
//
// Account.UserID, Account.Email and Account.Tenant are required, and the
// tenant has to be a valid one other than the visitors' queue and the team's.
// The answer is empty when there is nothing to move, so calling it twice moves
// nothing the second time, and two calls at the same instant move each ticket
// once. An error is a failure to read or write: when the move itself failed
// every ticket is where it was, and when the log refused an entry the tickets
// had already moved, as with every other act of this package. Without
// Config.VisitorTenant the answer is ErrVisitorsOff and nothing is read.
func (s *TicketService) AdoptVisitorTickets(ctx context.Context, account Account) ([]Summary, error) {
	if s.cfg.VisitorTenant == "" {
		return nil, ErrVisitorsOff
	}
	email := strings.ToLower(trimmed(account.Email))
	if account.UserID == "" {
		return nil, errors.New("ticket: AdoptVisitorTickets needs the account's user")
	}
	if !address(email) {
		return nil, errors.New("ticket: AdoptVisitorTickets needs the account's proven e-mail, and the one given is not an address")
	}
	if account.Tenant == s.cfg.VisitorTenant || account.Tenant == s.cfg.TeamTenant {
		return nil, fmt.Errorf("ticket: visitor tickets are adopted into an account's own space, and %q is the visitors' queue or the team's", account.Tenant)
	}
	from, err := adoptGrant(s.cfg.VisitorTenant)
	if err != nil {
		return nil, err
	}
	to, err := adoptGrant(account.Tenant)
	if err != nil {
		return nil, err
	}

	moved, skipped, err := s.adopt(ctx, from, to, account, email)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		s.cfg.Logger.Warn("ticket: visitor tickets that carry files were left in the visitors' queue", "tenant", account.Tenant, "user", account.UserID, "skipped", skipped)
	}
	actor := security.Subject{ID: account.UserID, Tenant: account.Tenant}
	out := make([]Summary, 0, len(moved))
	for _, m := range moved {
		if err := s.record(ctx, to, actor, ActivityTicketAdopted, m.ticket, map[string]any{"from_number": m.from, "origin": string(m.ticket.Origin)}); err != nil {
			return nil, err
		}
		out = append(out, s.mark(summaryOf(m.ticket)))
	}
	return out, nil
}

// received is what a visitor's message did: the ticket it opened or reached,
// and the message it wrote.
type received struct {
	ticket    *Ticket
	message   *Message
	appended  bool
	duplicate bool
}

// receive stores a visitor's message under the queue's Grant. Without a thread
// it opens a ticket, idempotent by what was said. With one, the open ticket of
// the thread takes it, and only when there is none is a ticket opened; the
// counter's row is held for the whole of it, so two messages of one
// conversation queue and the second finds the ticket the first opened.
func (c *core) receive(ctx context.Context, g security.Grant, d draft) (received, error) {
	if !d.Thread {
		made, err := c.create(ctx, g, d)
		if err != nil {
			return received{}, err
		}
		return received{ticket: made.ticket, message: made.message, duplicate: made.duplicate}, nil
	}
	var out received
	err := database.Transaction(ctx, c.db, func(ctx context.Context) error {
		now := c.now()
		if err := c.lock(ctx, g, now); err != nil {
			return err
		}
		current, err := Tickets(c.db).Where("origin_key", "=", d.OriginKey).Where("status", "<>", string(StatusClosed)).
			OrderByDesc("created_at").OrderByDesc("id").First(ctx, g)
		if err != nil {
			return err
		}
		if current == nil {
			made, err := c.create(ctx, g, d)
			if err != nil {
				return err
			}
			out = received{ticket: made.ticket, message: made.message}
			return nil
		}
		repeat, err := c.repeats(ctx, g, current, d.Body, now)
		if err != nil {
			return err
		}
		if repeat {
			out = received{ticket: current, appended: true, duplicate: true}
			return nil
		}
		author := d.Requester.Name
		if author == "" {
			author = current.RequesterName
		}
		reply, err := c.add(ctx, g, current, message{
			Role: RoleCustomer, AuthorID: current.RequesterID, AuthorName: author, Body: d.Body,
		}, current.ReceiveCustomerReply)
		if err != nil {
			return err
		}
		out = received{ticket: current, message: reply.message, appended: true}
		return nil
	})
	if err != nil {
		return received{}, err
	}
	return out, nil
}

// repeats says the customer's last message on the ticket, written inside the
// window, said the same words: the channel sent it twice.
func (c *core) repeats(ctx context.Context, g security.Grant, t *Ticket, body string, now time.Time) (bool, error) {
	last, err := Messages(c.db).Where("ticket_id", "=", t.ID).Where("internal", "=", 0).
		Where("created_at", ">=", now.Add(-c.cfg.ContactWindow)).OrderByDesc("created_at").OrderByDesc("id").First(ctx, g)
	if err != nil || last == nil {
		return false, err
	}
	return last.Role == RoleCustomer && squash(last.Body) == squash(body), nil
}

// adopted is a ticket that moved, and the number it had in the queue.
type adopted struct {
	ticket *Ticket
	from   int64
}

// adopt moves the queue's tickets of one e-mail into the account's space, in
// one transaction. The statement reads them by requester, which the e-mail
// decides, and the e-mail itself is compared again before anything moves.
func (c *core) adopt(ctx context.Context, from, to security.Grant, account Account, email string) ([]adopted, int, error) {
	var (
		out     []adopted
		skipped int
	)
	err := database.Transaction(ctx, c.db, func(ctx context.Context) error {
		now := c.now()
		found, err := Tickets(c.db).Where("requester_id", "=", visitorRequester(email)).OrderBy("created_at").OrderBy("id").Get(ctx, from)
		if err != nil {
			return err
		}
		for _, t := range found {
			if !strings.EqualFold(trimmed(t.RequesterEmail), email) {
				continue
			}
			files, err := Attachments(c.db).Where("ticket_id", "=", t.ID).Count(ctx, from)
			if err != nil {
				return err
			}
			if files > 0 {
				skipped++
				continue
			}
			moved, err := c.move(ctx, from, to, t, account, now)
			if err != nil {
				return err
			}
			if moved != nil {
				out = append(out, adopted{ticket: moved, from: t.Number})
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return out, skipped, nil
}

// move takes one ticket out of the queue and writes it, with its messages, in
// the account's space. A row does not change tenants: the Model refuses to
// write a tenant other than the Grant's, so the ticket is deleted under the
// queue's Grant and written again, with the same identifiers, under the
// account's. A ticket another adoption deleted first is not there to delete,
// and nil says so.
func (c *core) move(ctx context.Context, from, to security.Grant, t *Ticket, account Account, now time.Time) (*Ticket, error) {
	messages, err := Messages(c.db).Where("ticket_id", "=", t.ID).OrderBy("created_at").OrderBy("id").Get(ctx, from)
	if err != nil {
		return nil, err
	}
	gone, err := Tickets(c.db).WhereKey(t.ID).Delete(ctx, from)
	if err != nil {
		return nil, err
	}
	if gone == 0 {
		return nil, nil
	}
	if _, err := Messages(c.db).Where("ticket_id", "=", t.ID).Delete(ctx, from); err != nil {
		return nil, err
	}
	number, err := c.bump(ctx, to, now)
	if err != nil {
		return nil, err
	}

	name := clip(trimmed(account.Name), maxNameLen)
	if name == "" {
		name = t.RequesterName
	}
	row, err := Tickets(c.db).New()
	if err != nil {
		return nil, err
	}
	row.ID = t.ID
	row.TenantID = data.Tenant(to)
	row.Number = number
	row.Subject = t.Subject
	row.Status = t.Status
	row.Priority = t.Priority
	row.Category = t.Category
	row.RequesterID = account.UserID
	row.RequesterName = name
	row.RequesterEmail = clip(trimmed(account.Email), maxEmailLen)
	row.AssigneeID = t.AssigneeID
	row.AssigneeName = t.AssigneeName
	row.Origin = t.Origin
	row.OriginKey = t.OriginKey
	row.LastMessageAt = now
	row.CreatedAt = t.CreatedAt
	row.UpdatedAt = now
	row.ClosedAt = t.ClosedAt
	if _, err := row.Save(ctx, to); err != nil {
		return nil, err
	}

	for _, m := range messages {
		kept, err := Messages(c.db).New()
		if err != nil {
			return nil, err
		}
		kept.ID = m.ID
		kept.TenantID = data.Tenant(to)
		kept.TicketID = row.ID
		kept.AuthorID, kept.AuthorName = m.AuthorID, m.AuthorName
		// What the visitor wrote is the account's now.
		if m.Role == RoleCustomer && m.AuthorID == t.RequesterID {
			kept.AuthorID, kept.AuthorName = account.UserID, name
		}
		kept.Role = m.Role
		kept.Body = m.Body
		kept.Internal = m.Internal
		kept.CreatedAt = m.CreatedAt
		if _, err := kept.Save(ctx, to); err != nil {
			return nil, err
		}
	}
	if _, err := c.write(ctx, to, row, message{Role: RoleSystem, Body: c.labels.T("message.adopted")}, now); err != nil {
		return nil, err
	}
	return row, nil
}

// mark says whether a ticket is in the visitors' queue.
func (c *core) mark(s Summary) Summary {
	s.Visitor = c.cfg.VisitorTenant != "" && s.Tenant == c.cfg.VisitorTenant
	return s
}

// validateWords checks the subject and the body a channel carried, which are
// both required.
func (c *core) validateWords(errs validation.Errors, subject, body string) {
	switch {
	case subject == "":
		errs.Add("subject", c.labels.message("required"))
	case utf8.RuneCountInString(subject) > maxSubjectLen:
		errs.Add("subject", c.labels.message("max", map[string]any{"max": maxSubjectLen}))
	}
	switch {
	case body == "":
		errs.Add("body", c.labels.message("required"))
	case utf8.RuneCountInString(body) > MaxBodyChars:
		errs.Add("body", c.labels.message("max", map[string]any{"max": MaxBodyChars}))
	}
}

// address says the text is one bare e-mail address, no longer than the column:
// no display name, nothing around it.
func address(email string) bool {
	parsed, err := mail.ParseAddress(email)
	return err == nil && email != "" && parsed.Address == email && utf8.RuneCountInString(email) <= maxEmailLen
}

// visitorRequester is the requester of a visitor's tickets: the prefix and the
// first 32 hex digits of the SHA-256 of the identity, the e-mail in lower case
// or the thread. It is the same for every ticket of one address, which is what
// AdoptVisitorTickets reads by, and it says nothing of the address itself.
func visitorRequester(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return visitorPrefix + hex.EncodeToString(sum[:])[:32]
}

// threadKey is the digest a conversation is kept under: the origin and the
// thread, so the number of a WhatsApp conversation is never written in the
// clear, and the same text on another channel is another conversation.
func threadKey(origin Origin, thread string) string {
	return digest("thread", string(origin), thread)
}

// visitorKey identifies what a visitor said, as contactKey does for an
// account's form, with the origin in it: the same words on another channel are
// another request.
func visitorKey(origin Origin, email, subject, body string) string {
	return digest("visitor", string(origin), strings.ToLower(email), squash(subject), squash(body))
}

// digest is the SHA-256 of the parts, each ended by a zero byte, in hex.
func digest(parts ...string) string {
	sum := sha256.New()
	for _, part := range parts {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}
