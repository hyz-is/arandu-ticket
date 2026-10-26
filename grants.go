package ticket

import (
	"fmt"

	"github.com/arandu-io/framework/security"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// This file is the only place in the package that builds a Grant without a
// Policy answering for it, and the audit in tests/Unit holds that. Each of the
// three below is a Grant for work with no signed-in customer behind it, and
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
