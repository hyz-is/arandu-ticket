package ticket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// OpenFromContact opens a ticket from the application's public contact form,
// and only for somebody who has an account.
//
// The e-mail is asked of Config.Accounts. When it belongs to an account, the
// ticket opens in that account's space, with the user as its requester, the
// subject and the body as its first message and the contact form as its origin.
// When it does not, nothing is created and the answer is ErrNoAccount: the
// application keeps doing what it did with the form before, which is sending
// the e-mail.
//
// The same e-mail, subject and body sent twice inside Config.ContactWindow open
// one ticket: the second answer is the first ticket, with Duplicate set. Two
// requests at the same instant are told apart by the tenant's counter row, so
// neither can slip past the other.
//
// There is no session behind the form, so the Grant is the system's, scoped to
// the tenant the application's AccountResolver answered, and the sender is who
// the form says they are: anybody can type an address. The ticket therefore
// carries its origin, and a ticket the customer did not write is a ticket the
// customer can close. An application that must not accept a form from just
// anybody puts its own check -- a captcha, a rate limit -- in front of the call.
func (s *TicketService) OpenFromContact(ctx context.Context, in ContactInput) (ContactResult, error) {
	email := strings.ToLower(trimmed(in.Email))
	errs := validation.Errors{}
	if _, err := mail.ParseAddress(email); err != nil || email == "" || utf8.RuneCountInString(email) > maxEmailLen {
		errs.Add("email", s.labels.message("email"))
	}
	subject := trimmed(in.Subject)
	switch {
	case subject == "":
		errs.Add("subject", s.labels.message("required"))
	case utf8.RuneCountInString(subject) > maxSubjectLen:
		errs.Add("subject", s.labels.message("max", map[string]any{"max": maxSubjectLen}))
	}
	body := trimmed(in.Body)
	switch {
	case body == "":
		errs.Add("body", s.labels.message("required"))
	case utf8.RuneCountInString(body) > MaxBodyChars:
		errs.Add("body", s.labels.message("max", map[string]any{"max": MaxBodyChars}))
	}
	if errs.Any() {
		return ContactResult{}, errs
	}
	if s.cfg.Accounts == nil {
		return ContactResult{}, errors.New("ticket: OpenFromContact needs Config.Accounts, the application's AccountResolver")
	}

	account, err := s.cfg.Accounts.Resolve(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNoAccount) {
			return ContactResult{}, ErrNoAccount
		}
		return ContactResult{}, err
	}
	if account.UserID == "" {
		return ContactResult{}, fmt.Errorf("ticket: the account resolver answered an account with no user")
	}
	g, err := contactGrant(account.Tenant)
	if err != nil {
		return ContactResult{}, err
	}

	name := trimmed(account.Name)
	if name == "" {
		name = trimmed(in.Name)
	}
	address := trimmed(account.Email)
	if address == "" {
		address = email
	}
	requester := Person{ID: account.UserID, Name: clip(name, maxNameLen), Email: clip(address, maxEmailLen)}

	made, err := s.create(ctx, g, draft{
		Subject: subject, Body: body, Origin: OriginContactForm, OriginKey: contactKey(email, subject, body), Requester: requester,
	})
	if err != nil {
		return ContactResult{}, err
	}
	if made.duplicate {
		return ContactResult{Ticket: made.ticket, Duplicate: true}, nil
	}
	actor := security.Subject{ID: account.UserID, Tenant: account.Tenant}
	if err := s.record(ctx, g, actor, ActivityTicketOpened, made.ticket, map[string]any{"origin": string(OriginContactForm)}); err != nil {
		return ContactResult{}, err
	}
	s.notify(ctx, EventOpened, summaryOf(made.ticket), messageViewOf(made.message, nil))
	return ContactResult{Ticket: made.ticket}, nil
}

// contactKey identifies what a form said, so the same form sent twice is
// recognized: the e-mail without its case, and the subject and the body with
// every run of white space made one space, so a resubmission that differs by a
// trailing newline is the same words.
func contactKey(email, subject, body string) string {
	sum := sha256.New()
	for _, part := range []string{strings.ToLower(email), squash(subject), squash(body)} {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// squash makes every run of white space one space.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
