package ticket

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/filesystem"
)

// attachmentTypes are the kinds of file a message carries, by what the bytes
// are -- as detected, not as announced -- and the extension each is kept with.
// An image, a PDF and plain text: nothing a browser would run if it were
// served back, and the download route serves every one as an attachment, with
// the detected type and nosniff.
var attachmentTypes = map[string]string{
	"image/png":       ".png",
	"image/jpeg":      ".jpg",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"application/pdf": ".pdf",
	"text/plain":      ".txt",
}

// AttachmentTypes are the content types a message may carry, sorted.
func AttachmentTypes() []string {
	out := make([]string, 0, len(attachmentTypes))
	for kind := range attachmentTypes {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// pendingFile is a file already on the Disk whose row is not written yet.
type pendingFile struct {
	id, key, name, contentType, sha256 string
	size                               int64
}

// putFiles checks the uploads and keeps them on the Disk, under keys the server
// generates: the key holds a random identifier and the digest of the bytes, and
// nothing the sender typed. It returns nothing and stores nothing when any one
// of them is refused.
func (c *core) putFiles(ctx context.Context, g security.Grant, uploads []Upload) ([]pendingFile, error) {
	if len(uploads) == 0 {
		return nil, nil
	}
	if c.cfg.Files == nil {
		return nil, c.labels.invalid("files", "files_off")
	}
	if len(uploads) > c.cfg.MaxAttachments {
		return nil, c.labels.invalid("files", "files_many", map[string]any{"max": c.cfg.MaxAttachments})
	}

	type checked struct {
		body []byte
		file pendingFile
	}
	var all []checked
	for _, upload := range uploads {
		if upload.Body == nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(upload.Body, c.cfg.MaxAttachmentBytes+1))
		if err != nil {
			return nil, err
		}
		switch {
		case len(body) == 0:
			return nil, c.labels.invalid("files", "file_empty")
		case int64(len(body)) > c.cfg.MaxAttachmentBytes:
			return nil, c.labels.invalid("files", "file_large", map[string]any{"max": megabytes(c.cfg.MaxAttachmentBytes)})
		}
		kind, _, _ := strings.Cut(http.DetectContentType(body), ";")
		extension, allowed := attachmentTypes[strings.TrimSpace(kind)]
		if !allowed {
			return nil, c.labels.invalid("files", "file_type")
		}
		id, err := database.NewOrderedID()
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])
		all = append(all, checked{body: body, file: pendingFile{
			id:          id,
			key:         path.Join("ticket", "attachments", id, digest+extension),
			name:        fileName(upload.Name, extension),
			contentType: strings.TrimSpace(kind),
			sha256:      digest,
			size:        int64(len(body)),
		}})
	}

	stored := make([]pendingFile, 0, len(all))
	for _, one := range all {
		if err := c.cfg.Files.Put(ctx, g, one.file.key, bytes.NewReader(one.body), one.file.contentType); err != nil {
			c.dropFiles(ctx, g, stored)
			return nil, err
		}
		stored = append(stored, one.file)
	}
	return stored, nil
}

// dropFiles removes files whose rows were never written. A failure to remove
// one is logged and nothing else: the file is an orphan nobody can reach, and
// the request has already failed for a better reason.
func (c *core) dropFiles(ctx context.Context, g security.Grant, files []pendingFile) {
	for _, file := range files {
		if err := c.cfg.Files.Delete(ctx, g, file.key); err != nil {
			c.cfg.Logger.Warn("ticket: removing an orphaned attachment", "attachment", file.id, "error", err)
		}
	}
}

// open reads a file of a message the caller may read, and records that it was
// read. The team reads every message's; the customer's statement refuses a file
// of an internal note as it refuses one that is not there.
func (c *core) open(ctx context.Context, g security.Grant, actor security.Subject, t *Ticket, attachmentID string, team bool) (*Attachment, filesystem.File, error) {
	file, err := Attachments(c.db).WhereKey(attachmentID).Where("ticket_id", "=", t.ID).First(ctx, g)
	if err != nil {
		return nil, filesystem.File{}, err
	}
	if file == nil {
		return nil, filesystem.File{}, ErrNotFound
	}
	if !team {
		owner, err := Messages(c.db).WhereKey(file.MessageID).First(ctx, g)
		if err != nil {
			return nil, filesystem.File{}, err
		}
		if owner == nil || bool(owner.Internal) {
			return nil, filesystem.File{}, ErrNotFound
		}
	}
	if c.cfg.Files == nil {
		return nil, filesystem.File{}, fmt.Errorf("ticket: no Disk was configured for the attachments (Config.Files)")
	}
	body, err := c.cfg.Files.Get(ctx, g, file.Key)
	if err != nil {
		return nil, filesystem.File{}, err
	}
	side := "customer"
	if team {
		side = "team"
	}
	recorder, err := recordGrant(t.TenantID)
	if err != nil {
		_ = body.Body.Close()
		return nil, filesystem.File{}, err
	}
	if err := c.record(ctx, recorder, actor, ActivityAttachmentRead, t, map[string]any{"attachment_id": file.ID, "side": side}); err != nil {
		_ = body.Body.Close()
		return nil, filesystem.File{}, err
	}
	return file, body, nil
}

// fileName is the name a file is shown under: the last element of what the
// sender called it, without control characters, cut to the width of the
// column. A name that is nothing is called after what the file is.
func fileName(given, extension string) string {
	given = strings.ReplaceAll(given, `\`, "/")
	given = path.Base(given)
	if given == "." || given == "/" {
		given = ""
	}
	given = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, given)
	given = strings.TrimSpace(given)
	if given == "" {
		return "arquivo" + extension
	}
	if utf8.RuneCountInString(given) > maxFileNameLen {
		given = string([]rune(given)[:maxFileNameLen])
	}
	return given
}

// megabytes is a size as the sentence says it: "5" for five mebibytes.
func megabytes(n int64) string {
	mb := float64(n) / float64(1<<20)
	if mb == float64(int64(mb)) {
		return fmt.Sprintf("%d", int64(mb))
	}
	return fmt.Sprintf("%.1f", mb)
}
