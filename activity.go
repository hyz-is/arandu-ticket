package ticket

import (
	"context"
	"strings"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/translation"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// ActivityLogName is the log every entry of this package is recorded in.
const ActivityLogName = "ticket"

// ActivityRefType is the kind of subject an entry about a ticket names.
const ActivityRefType = "ticket"

// The events this package records, one per act that changes something, and one
// for a file being read. The subject of each entry is the ticket.
//
// No entry carries the text of a message, and the entry of an internal note
// carries nothing of the note: that an act happened is what the log keeps, and
// what was said stays in the conversation, where the side that may read it
// reads it.
const (
	ActivityTicketOpened          = "ticket.opened"
	ActivityTicketReplied         = "ticket.replied"
	ActivityTicketStatusChanged   = "ticket.status_changed"
	ActivityTicketPriorityChanged = "ticket.priority_changed"
	ActivityTicketAssigned        = "ticket.assigned"
	ActivityTicketClosed          = "ticket.closed"
	ActivityTicketReopened        = "ticket.reopened"
	ActivityTicketNoteAdded       = "ticket.note_added"
	ActivityAttachmentRead        = "ticket.attachment_read"
)

// record writes one entry of the act just done, under the Grant it was
// authorized with: the tenant of the entry is the act's, data.Tenant(g), and
// the causer is the subject who acted, named explicitly because a team
// member's act is written under a Grant scoped to the customer's tenant, whose
// own subject is nobody. Without Config.Activity it records nothing.
//
// The act already happened when this runs, so a failure to record it is
// returned to the caller rather than swallowed: an act the log does not show
// is the one an audit asks about.
func (c *core) record(ctx context.Context, g security.Grant, actor security.Subject, event string, t *Ticket, properties map[string]any) error {
	if c.cfg.Activity == nil {
		return nil
	}
	description := c.labels.line("activity."+strings.ReplaceAll(event, ".", "_"), translation.Replace{"code": t.Code()})
	entry := c.cfg.Activity.In(ctx, g, ActivityLogName).Event(event).
		OnRef(activitylog.Ref{Type: ActivityRefType, ID: t.ID})
	if actor.ID != "" {
		entry = entry.ByRef(activitylog.Ref{Type: "user", ID: actor.ID})
	}
	props := map[string]any{"number": t.Number}
	for key, value := range properties {
		props[key] = value
	}
	_, err := entry.WithProperties(props).Log(description)
	return err
}

// notify tells the application's Notifier what happened. It runs after the
// change is stored and its failure is logged, not returned: see Notifier.
func (c *core) notify(ctx context.Context, kind EventKind, t Summary, m MessageView) {
	if c.cfg.Notifier == nil || m.Internal {
		return
	}
	if err := c.cfg.Notifier.Notify(ctx, Event{Kind: kind, Ticket: t, Message: m}); err != nil {
		c.cfg.Logger.Warn("ticket: the notifier failed after the change was stored", "event", string(kind), "ticket", t.ID, "error", err)
	}
}
