package feature_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"

	ticket "github.com/hyz-is/arandu-ticket"
)

// png is the smallest image the detector knows by its first bytes.
var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)

const pdf = "%PDF-1.4\n1 0 obj\n<<>>\nendobj\n"

func upload(name string, body []byte) ticket.Upload {
	return ticket.Upload{Name: name, Body: bytes.NewReader(body)}
}

func TestAMessageCarriesFilesKeptUnderKeysTheServerMade(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana := member("acme", "user-ana")

	record, err := w.tickets.Open(ctx, ana, ticket.OpenRequest{Subject: "com anexo", Body: "veja"}, []ticket.Upload{
		upload("../../etc/passwd\\foto.png", png), upload("laudo.pdf", []byte(pdf)),
	})
	if err != nil {
		t.Fatalf("opening with files: %v", err)
	}
	conv, err := w.tickets.Find(ctx, ana, record.ID)
	if err != nil || len(conv.Messages[0].Attachments) != 2 {
		t.Fatalf("the first message carries %+v, %v", conv.Messages[0].Attachments, err)
	}
	first, second := conv.Messages[0].Attachments[0], conv.Messages[0].Attachments[1]
	if first.Name != "foto.png" || first.ContentType != "image/png" || second.ContentType != "application/pdf" {
		t.Fatalf("the files are %+v and %+v; the name is a bare name and the type is the one detected", first, second)
	}

	// The key is the server's: it holds an identifier and a digest, and
	// nothing the sender typed.
	var key, name string
	if err := w.sql.QueryRow(`SELECT file_key, file_name FROM ticket_attachments WHERE id = ?`, first.ID).Scan(&key, &name); err != nil {
		t.Fatalf("reading the row: %v", err)
	}
	if !strings.HasPrefix(key, "ticket/attachments/"+first.ID+"/") || !strings.HasSuffix(key, ".png") || strings.Contains(key, "passwd") || strings.Contains(key, "foto") {
		t.Fatalf("the key is %q", key)
	}

	file, body, err := w.tickets.Attachment(ctx, ana, record.ID, first.ID)
	if err != nil {
		t.Fatalf("opening the file: %v", err)
	}
	bytesRead, _ := io.ReadAll(body.Body)
	body.Body.Close()
	if !bytes.Equal(bytesRead, png) || file.Name != "foto.png" || file.Size != int64(len(png)) {
		t.Fatalf("the file read back is %q (%d bytes), want what was sent", file.Name, len(bytesRead))
	}

	// Reading it is recorded, with the reader's name.
	var downloads int
	for _, e := range entries(t, w, "acme") {
		if e.Event == "ticket.attachment_read" {
			downloads++
			if e.Causer != "user-ana" || !strings.Contains(e.Properties, first.ID) {
				t.Fatalf("the entry of the download is %+v", e)
			}
		}
	}
	if downloads != 1 {
		t.Fatalf("the log holds %d downloads, want one", downloads)
	}

	// A file of another ticket is not found under this ticket's address.
	other := open(t, w, ana, "outro", "x")
	if _, _, err := w.tickets.Attachment(ctx, ana, other.ID, first.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("a file under another ticket's address: %v", err)
	}
	// Nor is it the property of anybody else.
	if _, _, err := w.tickets.Attachment(ctx, member("acme", "user-bia"), record.ID, first.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("somebody else opened the file: %v", err)
	}
	if _, _, err := w.tickets.Attachment(ctx, member("globex", "user-gil"), record.ID, first.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("another space opened the file: %v", err)
	}
}

func TestAFileIsLookedAtBeforeItIsKept(t *testing.T) {
	t.Parallel()
	w := installWith(t, func(c *ticket.Config) { c.MaxAttachmentBytes = 1024; c.MaxAttachments = 2 })
	ctx := context.Background()
	ana := member("acme", "user-ana")
	record := open(t, w, ana, "arquivos", "x")

	var errs validation.Errors
	for name, c := range map[string]struct {
		uploads []ticket.Upload
	}{
		"a script announced as an image": {[]ticket.Upload{upload("a.png", []byte("<script>alert(1)</script>"))}},
		"an executable":                  {[]ticket.Upload{upload("a.pdf", []byte("MZ\x90\x00\x03\x00\x00\x00"))}},
		"a page":                         {[]ticket.Upload{upload("a.txt", []byte("<!DOCTYPE html><html><body>x</body></html>"))}},
		"an empty file":                  {[]ticket.Upload{upload("a.png", nil)}},
		"a file past the limit":          {[]ticket.Upload{upload("a.png", append(append([]byte(nil), png...), bytes.Repeat([]byte{1}, 2048)...))}},
		"too many files":                 {[]ticket.Upload{upload("1.png", png), upload("2.png", png), upload("3.png", png)}},
	} {
		_, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "oi"}, c.uploads)
		if !errors.As(err, &errs) || len(errs["files"]) == 0 {
			t.Errorf("%s answered %v, want a refusal on files", name, err)
		}
	}
	var stored int
	if err := w.sql.QueryRow(`SELECT COUNT(*) FROM ticket_attachments`).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("refused files left %d rows behind, %v", stored, err)
	}
	conv, _ := w.tickets.Find(ctx, ana, record.ID)
	if len(conv.Messages) != 1 {
		t.Fatalf("a refused file left %d messages, want the first only", len(conv.Messages))
	}

	// Plain text is kept as text, whatever it is called.
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{Body: "log"}, []ticket.Upload{upload("anotacoes.html", []byte("linha um\nlinha dois\n"))}); err != nil {
		t.Fatalf("plain text: %v", err)
	}
	conv, _ = w.tickets.Find(ctx, ana, record.ID)
	if got := conv.Messages[1].Attachments[0]; got.ContentType != "text/plain" || got.Name != "anotacoes.html" {
		t.Fatalf("the text file is %+v", got)
	}

	// A file alone is a message.
	if _, err := w.tickets.Reply(ctx, ana, record.ID, ticket.ReplyRequest{}, []ticket.Upload{upload("foto.png", png)}); err != nil {
		t.Fatalf("a file with no words: %v", err)
	}
}

func TestWithoutADiskNoFileIsTaken(t *testing.T) {
	t.Parallel()
	w := installWith(t, func(c *ticket.Config) { c.Files = nil })
	ctx := context.Background()

	var errs validation.Errors
	_, err := w.tickets.Open(ctx, member("acme", "user-ana"), ticket.OpenRequest{Subject: "x", Body: "y"}, []ticket.Upload{upload("a.png", png)})
	if !errors.As(err, &errs) || len(errs["files"]) == 0 {
		t.Fatalf("a file with no Disk answered %v", err)
	}
	if n := countTickets(t, w); n != 0 {
		t.Fatalf("a refused file left %d tickets", n)
	}
	if _, err := w.tickets.Open(ctx, member("acme", "user-ana"), ticket.OpenRequest{Subject: "x", Body: "y"}, nil); err != nil {
		t.Fatalf("a ticket with no file and no Disk: %v", err)
	}
}

func TestTheFilesOfAnInternalNoteAreTheTeamsAlone(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	ana, rita := member("acme", "user-ana"), agent("team-1")
	record := open(t, w, ana, "nota com anexo", "x")

	note, err := w.team.Note(ctx, rita, record.ID, ticket.ReplyRequest{Body: "print do painel interno"}, []ticket.Upload{upload("interno.png", png)})
	if err != nil {
		t.Fatalf("the note: %v", err)
	}
	var id string
	if err := w.sql.QueryRow(`SELECT id FROM ticket_attachments WHERE message_id = ?`, note.ID).Scan(&id); err != nil {
		t.Fatalf("finding the file: %v", err)
	}

	// The customer is told nothing: the file is not found, as it is not for
	// anyone with a file under the wrong address.
	if _, _, err := w.tickets.Attachment(ctx, ana, record.ID, id); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("the customer opened the file of a note: %v", err)
	}
	conv, _ := w.tickets.Find(ctx, ana, record.ID)
	if len(conv.Messages) != 1 || len(conv.Messages[0].Attachments) != 0 {
		t.Fatalf("the customer's conversation shows %+v", conv.Messages)
	}

	// The team opens it, from the customer's space, and it is recorded.
	_, body, err := w.team.Attachment(ctx, rita, record.ID, id)
	if err != nil {
		t.Fatalf("the team opening the file: %v", err)
	}
	body.Body.Close()
	if got := strings.Join(events(t, w, "acme"), ","); got != "ticket.opened,ticket.note_added,ticket.attachment_read" {
		t.Fatalf("the log holds %q", got)
	}
	last := entries(t, w, "acme")[2]
	if last.Causer != "team-1" || !strings.Contains(last.Properties, `"side":"team"`) {
		t.Fatalf("the entry of the download is %+v", last)
	}
	if _, _, err := w.team.Attachment(ctx, security.Subject{ID: "x", Tenant: realm, Actions: []security.Action{ticket.TicketManage}}, record.ID, id); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("somebody who may not read opened the file: %v", err)
	}
}
