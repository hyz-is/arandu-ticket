package ticket

import (
	"context"
	"errors"
	"io"
	"time"
)

// The three things the application provides. This package knows no table of
// users, sends no e-mail of its own and keeps no list of the team: each of
// those is an interface the application implements with what it already has,
// and passes in Config.

// ErrNoAccount is what an AccountResolver answers when no account has the
// e-mail, and what OpenFromContact returns for it: the contact form opens a
// ticket only for somebody who has an account, and for anybody else the
// application keeps doing what it did before.
var ErrNoAccount = errors.New("ticket: no account has that e-mail")

// Account is the user an e-mail belongs to, and the tenant they are a member
// of.
type Account struct {
	// UserID is the user. It becomes the requester of the ticket.
	UserID string
	// Tenant is the space the user's tickets live in. It is the application's
	// answer and nothing a request names, and it has to be a valid tenant.
	Tenant string
	// Name and Email are how the user is called and reached. Email is the one
	// the account has, which can differ in case from the one the form carried.
	Name  string
	Email string
}

// AccountResolver finds the account an e-mail belongs to.
//
// It is the application's: it reads its own users. OpenFromContact asks it and
// trusts the answer, so it answers only for an address that is an account's
// own, and ErrNoAccount for any other.
type AccountResolver interface {
	// Resolve returns the account that has the e-mail, or ErrNoAccount.
	Resolve(ctx context.Context, email string) (Account, error)
}

// Person is somebody the application knows, as the screens name them.
type Person struct {
	ID    string
	Name  string
	Email string
}

// Directory names people. It is optional: without one a ticket keeps the name
// and the e-mail of its requester empty when the application did not supply
// them, a message shows its author's role instead of a name, and a ticket can
// be assigned only to the member of the team who is asking.
type Directory interface {
	// Person returns a user of a tenant, or ErrNoAccount when there is none.
	Person(ctx context.Context, tenant, id string) (Person, error)
	// Team returns the members of the platform team a ticket can be assigned
	// to, in the order a list offers them.
	Team(ctx context.Context) ([]Person, error)
}

// EventKind says what happened on a ticket that somebody should be told about.
type EventKind string

// The events a Notifier is told about.
const (
	// EventOpened is a ticket a customer opened, on the screens or through the
	// contact form. The team is the one to tell.
	EventOpened EventKind = "ticket.opened"
	// EventCustomerReplied is a reply from the customer. The team is the one to
	// tell.
	EventCustomerReplied EventKind = "ticket.customer_replied"
	// EventTeamReplied is an answer from the team. The customer is the one to
	// tell: the requester of the ticket.
	EventTeamReplied EventKind = "ticket.team_replied"
)

// ForTeam says whether the team is the one to tell. When it is not, the
// requester of the ticket is.
func (k EventKind) ForTeam() bool { return k == EventOpened || k == EventCustomerReplied }

// Event is what a Notifier is handed.
//
// An internal note is never one: a note changes nothing the customer can read,
// and the package tells nobody about it.
type Event struct {
	Kind EventKind
	// Ticket is the ticket after the change.
	Ticket Summary
	// Message is the message the event is about. It is never an internal one.
	Message MessageView
}

// Notifier tells people that something happened on a ticket.
//
// The application implements it with its own mailer: this package sends no
// e-mail. Notify runs after the change is stored, and its error does not undo
// the change: the ticket is already open and the reply already written, and a
// mail server that is down is no reason for a customer to write it twice. The
// error is logged and the request goes on. An application that must not lose a
// notification enqueues it here and delivers it from its queue.
type Notifier interface {
	Notify(ctx context.Context, e Event) error
}

// Upload is a file sent with a message.
type Upload struct {
	// Name is the file's name as the sender had it. Only its last element is
	// kept.
	Name string
	// Body is the file's bytes. The package reads at most Config.MaxAttachmentBytes
	// of it, and decides what it is by looking, not by what it is called.
	Body io.Reader
}

// OpenRequest is what a customer asks for when opening a ticket on the screens.
// There is no tenant, no requester and no priority here and there must never
// be one: the tenant and the requester come from the Grant and the subject, and
// the priority is the team's to set.
type OpenRequest struct {
	Subject  string
	Category string
	Body     string
}

// ReplyRequest is a message added to a ticket.
type ReplyRequest struct {
	Body string
}

// ContactInput is what the application's public contact form carries.
type ContactInput struct {
	// Email is the address the sender wrote. The ticket opens only if it is an
	// account's.
	Email string
	// Name is what the sender called themselves; the account's own name wins
	// when it has one.
	Name    string
	Subject string
	Body    string
}

// ContactResult is what OpenFromContact answers.
type ContactResult struct {
	// Ticket is the ticket the form opened, or the one it already had opened
	// when this is a repeat.
	Ticket *Ticket
	// Duplicate says the same e-mail, subject and body had already opened a
	// ticket inside Config.ContactWindow, and nothing new was written.
	Duplicate bool
}

// ListQuery narrows the customer's list. Page counts from one.
type ListQuery struct {
	Search   string
	Statuses []Status
	Sort     string
	Dir      string
	Page     int
	PerPage  int
}

// TeamQuery narrows the team's list. Page counts from one.
type TeamQuery struct {
	// Search matches the number, the subject and the requester's name and
	// e-mail.
	Search     string
	Statuses   []Status
	Priorities []Priority
	// Assignee is a team member's identifier, AssigneeNone for the tickets
	// nobody took, or empty for every ticket.
	Assignee string
	// Tenant narrows the list to one space.
	Tenant  string
	Sort    string
	Dir     string
	Page    int
	PerPage int
}

// AssigneeNone is TeamQuery.Assignee for the tickets nobody took.
const AssigneeNone = "none"

// Page is one page of a list, and how many rows the whole list has.
type Page struct {
	Rows  []Summary
	Total int
	Page  int
	Pages int
}

// pagesOf is how many pages of perPage rows hold total rows, at least one.
func pagesOf(total, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 1
	}
	return (total + perPage - 1) / perPage
}

// clock is what the services read the time from.
type clock func() time.Time
