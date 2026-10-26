package ticket

import (
	"context"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// Migrations declares the schema this module owns.
//
// They are returned in the order their names sort in, which is the order they
// apply in: the name carries the order, and nothing else decides it.
func (m *Module) Migrations() []foundation.Migration {
	return []foundation.Migration{createTickets{}}
}

// The migration is reversible, and the assertion is here rather than discovered
// at rollback: the migrator tests for Down with a type assertion, so a Down
// with the wrong signature would leave a rollback that silently does nothing.
var _ migrations.ReversibleMigration = createTickets{}

// createTickets is the four tables this module owns, and the indexes its
// listings read by.
type createTickets struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order. It is fixed
// once the package is published: changing what an applied name means leaves the
// change missing everywhere it already ran, and nothing says so.
func (createTickets) GetName() string { return "20261026_0001_create_tickets" }

// Up creates the tables and the indexes.
//
// The Blueprint spells each column for the engine the migration is running on,
// which is what lets one application develop on a file and deploy on Postgres
// without a second schema. The timestamps have no database default: the value
// comes from Go, which is also what lets a test hold the clock.
func (createTickets) Up(ctx context.Context, conn migrations.Connection) error {
	if err := conn.Schema().Create(ctx, countersTable, func(table *schema.Blueprint) {
		// The key is the tenant: one row each, which is what the numbers are
		// taken from and what a transaction locks to take them in turn.
		table.String("id").Primary()
		table.String("tenant_id")
		table.BigInteger("last_number").Default(0)
		table.Timestamp("updated_at")
	}); err != nil {
		return err
	}

	if err := conn.Schema().Create(ctx, ticketsTable, func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.BigInteger("number")
		table.String("subject", maxSubjectLen)
		table.String("status", 32)
		table.String("priority", 16)
		table.String("category", maxCategoryLen).Default("")
		table.String("requester_id", maxIdentifierLen)
		table.String("requester_name", maxNameLen).Default("")
		table.String("requester_email", maxEmailLen).Default("")
		table.String("assignee_id", maxIdentifierLen).Default("")
		table.String("assignee_name", maxNameLen).Default("")
		table.String("origin", 32)
		table.String("origin_key", maxHashLen).Default("")
		table.Timestamp("last_message_at")
		table.Timestamp("created_at")
		table.Timestamp("updated_at")
		table.Timestamp("closed_at").Nullable()

		// The number is the ticket's name inside its tenant, and two tickets
		// cannot share it: this is the net under the counter.
		table.Unique([]string{"tenant_id", "number"}, "tickets_tenant_number_unique")
		// The indexes match the ORDER BY of the listings, tenant first for the
		// customer's and without it for the team's. Without them every page is a
		// scan of every customer's rows.
		table.Index([]string{"tenant_id", "updated_at", "id"}, "tickets_tenant_updated_idx")
		table.Index([]string{"tenant_id", "requester_id", "updated_at"}, "tickets_tenant_requester_idx")
		table.Index([]string{"tenant_id", "origin_key", "created_at"}, "tickets_tenant_origin_idx")
		table.Index([]string{"status", "updated_at"}, "tickets_status_updated_idx")
		table.Index([]string{"assignee_id", "updated_at"}, "tickets_assignee_updated_idx")
	}); err != nil {
		return err
	}

	if err := conn.Schema().Create(ctx, messagesTable, func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.String("ticket_id")
		table.String("author_id", maxIdentifierLen).Default("")
		table.String("author_name", maxNameLen).Default("")
		table.String("role", 16)
		table.Text("body")
		table.Integer("internal").Default(0)
		table.Timestamp("created_at")

		table.Index([]string{"tenant_id", "ticket_id", "created_at", "id"}, "ticket_messages_conversation_idx")
	}); err != nil {
		return err
	}

	return conn.Schema().Create(ctx, attachmentsTable, func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.String("ticket_id")
		table.String("message_id")
		table.String("file_key", maxKeyLen)
		table.String("file_name", maxNameLen)
		table.String("content_type", 64)
		table.BigInteger("size")
		table.String("sha256", maxHashLen)
		table.Timestamp("created_at")

		table.Index([]string{"tenant_id", "ticket_id", "created_at", "id"}, "ticket_attachments_ticket_idx")
	})
}

// Down drops the tables, which takes their indexes with them.
func (createTickets) Down(ctx context.Context, conn migrations.Connection) error {
	for _, table := range []string{attachmentsTable, messagesTable, ticketsTable, countersTable} {
		if err := conn.Schema().DropIfExists(ctx, table); err != nil {
			return err
		}
	}
	return nil
}
