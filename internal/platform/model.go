// Package platform is the tickets table read without the tenant filter: the one
// read of arandu-ticket that crosses tenants, the team's list and the lookup
// that finds which tenant a ticket is in, after the team's policy allowed it.
//
// It is internal so no application can import it. Its table is Global, which is
// how Hesape spells a table with no tenant filter, and any Grant at all would
// read every tenant through it; the package that may reach it is the one whose
// callers have asked the team's policy first. Its rows never leave them: they
// are copied into snapshots that hold no connection, so nothing read across
// tenants can be saved or deleted through it, and every write to a ticket goes
// through the tenant's own model under a Grant scoped to that tenant.
package platform

import (
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// Ticket is a row of the tickets table, of any tenant. It declares the columns
// a list shows and nothing else: a conversation never travels with it.
type Ticket struct {
	model.Model

	ID             string     `db:"id"`
	TenantID       string     `db:"tenant_id"`
	Number         int64      `db:"number"`
	Subject        string     `db:"subject"`
	Status         string     `db:"status"`
	Priority       string     `db:"priority"`
	Category       string     `db:"category"`
	RequesterID    string     `db:"requester_id"`
	RequesterName  string     `db:"requester_name"`
	RequesterEmail string     `db:"requester_email"`
	AssigneeID     string     `db:"assignee_id"`
	AssigneeName   string     `db:"assignee_name"`
	Origin         string     `db:"origin"`
	LastMessageAt  time.Time  `db:"last_message_at"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
	ClosedAt       *time.Time `db:"closed_at"`
}

// ticketTable is the tickets table, Global: no tenant filter on any statement.
// The name is written here rather than imported, because the package that owns
// the constant imports this one.
var ticketTable = model.NewTable(model.TableSpec{
	Name:         "tickets",
	New:          func() model.Entity { return new(Ticket) },
	ManualKey:    true,
	NoTimestamps: true,
	Global:       true,
})

// Columns are the columns Ticket reads.
var Columns = []any{
	"id", "tenant_id", "number", "subject", "status", "priority", "category", "requester_id", "requester_name",
	"requester_email", "assignee_id", "assignee_name", "origin", "last_message_at", "created_at", "updated_at", "closed_at",
}
