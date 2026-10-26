package ticket

import (
	"context"
	"fmt"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database"
)

// draft is a ticket about to be opened. It holds what the request typed and the
// requester the application named, and no tenant: the tenant is the Grant's.
type draft struct {
	Subject, Category, Body string
	Origin                  Origin
	// OriginKey, when set, makes the opening idempotent: a ticket of the tenant
	// opened inside Config.ContactWindow with the same key is answered instead
	// of a new one.
	OriginKey string
	Requester Person
	Uploads   []Upload
}

// created is a ticket that was opened, or the one a repeat found.
type created struct {
	ticket    *Ticket
	message   *Message
	files     []*Attachment
	duplicate bool
}

// message is a message about to be added to a ticket.
type message struct {
	Role                       Role
	AuthorID, AuthorName, Body string
	Internal                   bool
	Uploads                    []Upload
}

// added is a message that was stored, with its files.
type added struct {
	message *Message
	files   []*Attachment
}

// create opens a ticket: the number, the row, the first message and its files,
// in one transaction under the Grant the caller holds. The files are stored
// first, under keys nobody can guess; if the rows that follow fail, they are
// orphans nobody can reach and are removed, and the other order would leave a
// message that points at nothing.
func (c *core) create(ctx context.Context, g security.Grant, d draft) (created, error) {
	now := c.now()
	files, err := c.putFiles(ctx, g, d.Uploads)
	if err != nil {
		return created{}, err
	}
	var out created
	err = database.Transaction(ctx, c.db, func(ctx context.Context) error {
		if d.OriginKey != "" {
			// The counter's row is the lock: two requests that carry the same
			// words queue here, and the second finds the first one's ticket.
			if err := c.lock(ctx, g, now); err != nil {
				return err
			}
			twin, err := c.twin(ctx, g, d.OriginKey, now)
			if err != nil {
				return err
			}
			if twin != nil {
				out = created{ticket: twin, duplicate: true}
				return nil
			}
		}
		number, err := c.bump(ctx, g, now)
		if err != nil {
			return err
		}
		t, err := Tickets(c.db).New()
		if err != nil {
			return err
		}
		if t.ID, err = data.NewID(); err != nil {
			return err
		}
		t.TenantID = data.Tenant(g)
		t.Number = number
		t.Subject = d.Subject
		t.Status = StatusOpen
		t.Priority = PriorityNormal
		t.Category = d.Category
		t.RequesterID = d.Requester.ID
		t.RequesterName = d.Requester.Name
		t.RequesterEmail = d.Requester.Email
		t.Origin = d.Origin
		t.OriginKey = d.OriginKey
		t.LastMessageAt = now
		t.CreatedAt = now
		t.UpdatedAt = now
		if _, err := t.Save(ctx, g); err != nil {
			return err
		}
		first, err := c.write(ctx, g, t, message{
			Role: RoleCustomer, AuthorID: d.Requester.ID, AuthorName: d.Requester.Name, Body: d.Body,
		}, now)
		if err != nil {
			return err
		}
		stored, err := c.attach(ctx, g, t, first, files, now)
		if err != nil {
			return err
		}
		out = created{ticket: t, message: first, files: stored}
		return nil
	})
	if err != nil {
		c.dropFiles(ctx, g, files)
		return created{}, err
	}
	if out.duplicate {
		c.dropFiles(ctx, g, files)
	}
	return out, nil
}

// add stores a message on a ticket the caller loaded and authorized. move is
// the ticket's own rule for what the message does to it, applied before
// anything is written, so a ticket that refuses the message refuses it with
// nothing stored.
func (c *core) add(ctx context.Context, g security.Grant, t *Ticket, m message, move func(time.Time) error) (added, error) {
	now := c.now()
	if err := move(now); err != nil {
		return added{}, err
	}
	files, err := c.putFiles(ctx, g, m.Uploads)
	if err != nil {
		return added{}, err
	}
	var out added
	err = database.Transaction(ctx, c.db, func(ctx context.Context) error {
		if _, err := t.Save(ctx, g); err != nil {
			return err
		}
		stored, err := c.write(ctx, g, t, m, now)
		if err != nil {
			return err
		}
		out.message = stored
		out.files, err = c.attach(ctx, g, t, stored, files, now)
		return err
	})
	if err != nil {
		c.dropFiles(ctx, g, files)
		return added{}, err
	}
	return out, nil
}

// write stores one message row. The tenant is the Grant's.
func (c *core) write(ctx context.Context, g security.Grant, t *Ticket, m message, now time.Time) (*Message, error) {
	row, err := Messages(c.db).New()
	if err != nil {
		return nil, err
	}
	if row.ID, err = database.NewOrderedID(); err != nil {
		return nil, err
	}
	row.TenantID = data.Tenant(g)
	row.TicketID = t.ID
	row.AuthorID = m.AuthorID
	row.AuthorName = m.AuthorName
	row.Role = m.Role
	row.Body = m.Body
	row.Internal = Flag(m.Internal)
	row.CreatedAt = now
	if _, err := row.Save(ctx, g); err != nil {
		return nil, err
	}
	return row, nil
}

// attach stores the rows of the files already on the Disk. The tenant is the
// Grant's.
func (c *core) attach(ctx context.Context, g security.Grant, t *Ticket, m *Message, files []pendingFile, now time.Time) ([]*Attachment, error) {
	out := make([]*Attachment, 0, len(files))
	for _, file := range files {
		row, err := Attachments(c.db).New()
		if err != nil {
			return nil, err
		}
		row.ID = file.id
		row.TenantID = data.Tenant(g)
		row.TicketID = t.ID
		row.MessageID = m.ID
		row.Key = file.key
		row.Name = file.name
		row.ContentType = file.contentType
		row.Size = file.size
		row.SHA256 = file.sha256
		row.CreatedAt = now
		if _, err := row.Save(ctx, g); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// ensure makes sure the tenant has a counter, and leaves it as it was when it
// has.
func (c *core) ensure(ctx context.Context, g security.Grant, now time.Time) error {
	tenant := data.Tenant(g)
	_, err := Counters(c.db).InsertOrIgnore(ctx, g, map[string]any{
		"id": tenant, "tenant_id": tenant, "last_number": 0, "updated_at": now,
	})
	return err
}

// lock takes the tenant's counter row for the rest of the transaction, without
// spending a number. Writing the row is what takes the lock on every engine:
// the second transaction to write it waits for the first to end.
func (c *core) lock(ctx context.Context, g security.Grant, now time.Time) error {
	if err := c.ensure(ctx, g, now); err != nil {
		return err
	}
	_, err := Counters(c.db).WhereKey(data.Tenant(g)).Update(ctx, g, map[string]any{"updated_at": now})
	return err
}

// bump spends the tenant's next number. It writes before it reads, so two
// transactions that come here at once are told apart by the row they wait on
// and each reads the number it spent. The unique index on (tenant_id, number)
// is the net under it.
func (c *core) bump(ctx context.Context, g security.Grant, now time.Time) (int64, error) {
	if err := c.ensure(ctx, g, now); err != nil {
		return 0, err
	}
	tenant := data.Tenant(g)
	if _, err := Counters(c.db).WhereKey(tenant).Increment(ctx, g, "last_number", 1, map[string]any{"updated_at": now}); err != nil {
		return 0, err
	}
	row, err := Counters(c.db).WhereKey(tenant).First(ctx, g)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, fmt.Errorf("ticket: the counter of the tenant %q is gone", tenant)
	}
	return row.LastNumber, nil
}

// twin is the ticket of the tenant that an identical opening made inside the
// window, or nil.
func (c *core) twin(ctx context.Context, g security.Grant, key string, now time.Time) (*Ticket, error) {
	return Tickets(c.db).Where("origin_key", "=", key).Where("created_at", ">=", now.Add(-c.cfg.ContactWindow)).
		OrderByDesc("created_at").OrderByDesc("id").First(ctx, g)
}

// conversation reads what was said on a ticket, oldest first. Internal notes
// are read only when the caller is the team: the customer's statement leaves
// them out in the query, and customerConversation drops them again on the way
// out.
func (c *core) conversation(ctx context.Context, g security.Grant, t *Ticket, internal bool) (Conversation, error) {
	messages := Messages(c.db).Where("ticket_id", "=", t.ID)
	if !internal {
		messages = messages.Where("internal", "=", 0)
	}
	read, err := messages.OrderBy("created_at").OrderBy("id").Get(ctx, g)
	if err != nil {
		return Conversation{}, err
	}
	files, err := Attachments(c.db).Where("ticket_id", "=", t.ID).OrderBy("created_at").OrderBy("id").Get(ctx, g)
	if err != nil {
		return Conversation{}, err
	}
	out := Conversation{Ticket: summaryOf(t), Messages: make([]MessageView, 0, len(read))}
	for _, m := range read {
		out.Messages = append(out.Messages, messageViewOf(m, files))
	}
	return out, nil
}
