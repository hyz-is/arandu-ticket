package ticket

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
)

// The actions of this package. Constants rather than strings at the call site:
// a typo in an action name would silently authorize nothing, or worse,
// everything.
//
// They carry the entity in the name because an application registers many
// packages, and the name of an action shows up in logs and in audit trails
// where "view" on its own says nothing about what was viewed.
//
// The actions are what an arandu-permission group carries: the application
// declares them in the catalogue (Actions and TeamActions), an organization or
// the platform grants them to whom it decides, and arandu-permission fills the
// subject's actions on every request. Nothing here is decided by a role.
const (
	// TicketCreate is opening a ticket in the subject's own space.
	TicketCreate security.Action = "ticket.create"
	// TicketViewOwn is reading the tickets the subject asked for themselves.
	TicketViewOwn security.Action = "ticket.view_own"
	// TicketViewSpace is reading every ticket of the subject's space, whoever
	// asked: for the people who run the space, beside those they run it for.
	TicketViewSpace security.Action = "ticket.view_space"
	// TicketReply is adding a message to a ticket the subject can read.
	TicketReply security.Action = "ticket.reply"
	// TicketClose is closing a ticket the subject can read, and reopening it.
	TicketClose security.Action = "ticket.close"

	// TicketViewAll is the team reading every ticket of every space, internal
	// notes included.
	TicketViewAll security.Action = "ticket.view_all"
	// TicketManage is the team answering a customer and changing a ticket's
	// status and priority.
	TicketManage security.Action = "ticket.manage"
	// TicketAssign is the team giving a ticket to a member of the team.
	TicketAssign security.Action = "ticket.assign"
	// TicketNote is the team writing a note only the team reads.
	TicketNote security.Action = "ticket.note"

	// TicketOpenFromContact is the application's contact form opening a ticket
	// for the account an e-mail belongs to. It is not a subject's action: no
	// person holds it, and OpenFromContact is the only place that issues it.
	TicketOpenFromContact security.Action = "ticket.open_from_contact"
)

// Actions are the customer's actions, for an application to declare in
// arandu-permission's catalogue beside its own, so an organization grants them
// to whom it decides.
func Actions() []security.Action {
	return []security.Action{TicketCreate, TicketViewOwn, TicketViewSpace, TicketReply, TicketClose}
}

// TeamActions are the platform team's actions. They are declared apart from
// Actions so a catalogue an organization grants from never holds them: an
// application gives them to the groups of its own team only, and TeamPolicy
// refuses them to any subject outside Config.TeamTenant besides.
func TeamActions() []security.Action {
	return []security.Action{TicketViewAll, TicketManage, TicketAssign, TicketNote}
}

// reads says an action only looks. Everything else is told to write, so an
// action added without a decision here fails the first time it tries to write
// under a name that says it only looks, and not the other way round.
func reads(a security.Action) bool {
	switch a {
	case TicketViewOwn, TicketViewSpace, TicketViewAll:
		return true
	}
	return false
}

// TicketPolicy is the only authority over what a customer does with a Ticket.
//
// It denies unless a rule below says otherwise, and every rule reads what the
// subject was granted: a subject with no actions, an administrator included,
// is refused everything. Ownership decides the rest -- a customer reads and
// answers the tickets they asked for, and the whole space's only with
// TicketViewSpace -- and the tenant of the record is checked first, before any
// rule, so a rule that opens an action opens it inside one space only.
type TicketPolicy struct{}

// Compile-time proof that the policy answers about this entity and no other. A
// policy that drifted onto another type would leave this one unguarded while
// the Model path still compiled.
var _ security.Policy[Ticket] = TicketPolicy{}

// Compile-time proof that the policy says which of its actions write: without
// it the Grant issued to read a ticket would also be one that writes.
var _ auth.Writes = TicketPolicy{}

// Writes reports whether a Grant issued for the action may change anything.
func (TicketPolicy) Writes(a security.Action) bool { return !reads(a) }

// Can decides whether the subject may perform the action on the record.
//
// It is the only place that decides. The service reaches the Model only after
// Authorize turns this method's nil result into a Grant.
//
// The record is the empty Ticket where the question is "may this subject do
// this kind of thing at all", and the loaded row where it is "may they do it to
// this ticket". Both are asked, in that order, and the second is what a rule
// about ownership answers.
func (TicketPolicy) Can(_ context.Context, s security.Subject, a security.Action, record Ticket) error {
	// Tenant isolation comes first and applies to every action. Without it every
	// check below would be pointless in a multi-tenant system: a rule that
	// allows an owner to read their own record would allow it across customers
	// as soon as two of them have a record with the same identifier.
	//
	// The empty id is the candidate that has not been stored yet, which belongs
	// to nobody until it is written with the tenant off the Grant.
	if record.ID != "" && record.TenantID != s.Tenant {
		return fmt.Errorf("ticket belongs to another tenant")
	}

	// arandu:begin custom
	if s.IsGuest() {
		return fmt.Errorf("a guest has no tickets")
	}
	if !s.Can(a) {
		return fmt.Errorf("the subject was not granted %s", a)
	}
	switch a {
	case TicketCreate:
		if record.ID == "" {
			return nil
		}
	case TicketViewSpace:
		return nil
	case TicketViewOwn:
		// The probe -- the empty record -- asks whether this kind of reading is
		// possible at all, and the service then narrows the statement to the
		// subject's own tickets. The second call, on the loaded row, is where
		// the ownership is decided.
		if record.ID == "" || record.RequesterID == s.ID {
			return nil
		}
	case TicketReply, TicketClose:
		if record.ID == "" || record.RequesterID == s.ID || s.Can(TicketViewSpace) {
			return nil
		}
	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on ticket", a)
}

// TeamPolicy is the only authority over what the platform team does with a
// Ticket of any space.
//
// It denies unless the subject is a member of the team -- a subject of Realm,
// the tenant the team signs in to -- who was granted the action. A subject of
// any other tenant is refused whatever actions it carries, and so is a subject
// somebody else is viewing as: an operator inside a customer's account acts as
// the customer and reads no other space.
//
// It is the one policy that does not compare a record's tenant with the
// subject's, because crossing tenants is what the team does. The tenant of the
// work itself is not the subject's: the service reads the ticket to find it,
// and then acts in that tenant under a Grant scoped to it, issued only after
// this policy allowed the action.
type TeamPolicy struct {
	// Realm is the tenant the team signs in to. Empty refuses everybody.
	Realm string
}

var _ security.Policy[Ticket] = TeamPolicy{}

var _ auth.Writes = TeamPolicy{}

// Writes reports whether a Grant issued for the action may change anything.
func (TeamPolicy) Writes(a security.Action) bool { return !reads(a) }

// Can decides whether the subject may perform the action.
func (p TeamPolicy) Can(_ context.Context, s security.Subject, a security.Action, _ Ticket) error {
	if p.Realm == "" || s.Tenant != p.Realm {
		return fmt.Errorf("only the platform team acts on every space's tickets")
	}

	// arandu:begin custom
	if s.IsGuest() {
		return fmt.Errorf("a guest is no member of the team")
	}
	if s.Impersonator != nil {
		return fmt.Errorf("a subject inside somebody else's account reads no other space")
	}
	switch a {
	case TicketViewAll, TicketManage, TicketAssign, TicketNote:
		if s.Can(a) {
			return nil
		}
		return fmt.Errorf("the subject was not granted %s", a)
	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s on ticket", a)
}
