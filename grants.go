package ticket

import (
	"fmt"

	"github.com/arandu-io/framework/security"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// This file is the only place in the package that builds a Grant without a
// Policy answering for it, and the audit in tests/Unit holds that. Each of the
// five below is a Grant for work with no signed-in customer behind it, and
// each is issued after something else decided:
//
//   - contactGrant, after the application's AccountResolver named the account,
//     for the contact form's one act.
//   - scopedGrant, after TeamPolicy allowed the team member, for the one tenant
//     the ticket they act on lives in. The Grant the team member holds is the
//     realm's; a ticket is in a customer's tenant, and a statement needs the
//     Grant of the tenant it reads. The scope is checked against the action
//     the realm Grant was issued for, so a Grant issued to read cannot be
//     scoped into one that writes something else.
//   - recordGrant, for the entry that says a file was read: reading changes no
//     ticket, and the entry is written by the log's own action.
//   - visitorGrant, after the application called OpenFromVisitor and turned
//     visitors on with Config.VisitorTenant, for the visitors' queue alone:
//     the tenant is the configuration's, never a value the visitor sent.
//   - adoptGrant, after the application proved that the account owns the
//     e-mail and called AdoptVisitorTickets, once for the visitors' queue the
//     tickets leave and once for the account's space they enter. The tenants
//     are the configuration's and the account's the application answered.

// contactGrant is the Grant the contact form's ticket is written with. The
// tenant is the one the application's AccountResolver answered for the
// account, never a value from the form.
func contactGrant(tenant string) (security.Grant, error) {
	if !security.ValidTenant(tenant) {
		return security.Grant{}, fmt.Errorf("ticket: the account resolver answered the tenant %q, which cannot be one", tenant)
	}
	//arandu:system-grant the contact form has no session; the tenant is the account's, answered by the application's AccountResolver
	g := security.SystemGrant(TicketOpenFromContact, tenant)
	if err := g.Check(TicketOpenFromContact); err != nil {
		return security.Grant{}, err
	}
	return g, nil
}

// scopedGrant scopes the realm Grant of an allowed team member to the tenant of
// one ticket. The realm Grant must have been issued for the action asked.
func scopedGrant(realm security.Grant, a security.Action, tenant string) (security.Grant, error) {
	if err := realm.Check(a); err != nil {
		return security.Grant{}, err
	}
	if !security.ValidTenant(tenant) {
		return security.Grant{}, fmt.Errorf("ticket: a ticket is in the tenant %q, which cannot be one", tenant)
	}
	//arandu:system-grant TeamPolicy allowed the team member in their realm; each read or write is scoped to the one tenant the ticket is in
	return security.SystemGrant(a, tenant), nil
}

// recordGrant is the Grant an entry that says a file was read is written with.
func recordGrant(tenant string) (security.Grant, error) {
	if !security.ValidTenant(tenant) {
		return security.Grant{}, fmt.Errorf("ticket: a ticket is in the tenant %q, which cannot be one", tenant)
	}
	//arandu:system-grant the read was authorized by the policy; the entry that records it is written with the log's own action
	return security.SystemGrant(activitylog.ActivityRecord, tenant), nil
}

// visitorGrant is the Grant a visitor's ticket is written with. The tenant is
// Config.VisitorTenant, which the application configured; nothing the visitor
// sent chooses it.
func visitorGrant(tenant string) (security.Grant, error) {
	if !security.ValidTenant(tenant) {
		return security.Grant{}, fmt.Errorf("ticket: Config.VisitorTenant is %q, which cannot be a tenant", tenant)
	}
	//arandu:system-grant a visitor has no session; the application called OpenFromVisitor, and the tenant is Config.VisitorTenant, which exists only when the application turned visitors on
	g := security.SystemGrant(TicketOpenFromVisitor, tenant)
	if err := g.Check(TicketOpenFromVisitor); err != nil {
		return security.Grant{}, err
	}
	return g, nil
}

// adoptGrant is the Grant a visitor's tickets are moved with: one for the
// visitors' queue they leave, one for the account's space they enter.
func adoptGrant(tenant string) (security.Grant, error) {
	if !security.ValidTenant(tenant) {
		return security.Grant{}, fmt.Errorf("ticket: tickets cannot be adopted into or out of the tenant %q, which cannot be one", tenant)
	}
	//arandu:system-grant the application proved the account owns the e-mail and called AdoptVisitorTickets; the tenants are Config.VisitorTenant and the account's, both answered by the application
	g := security.SystemGrant(TicketAdopt, tenant)
	if err := g.Check(TicketAdopt); err != nil {
		return security.Grant{}, err
	}
	return g, nil
}
