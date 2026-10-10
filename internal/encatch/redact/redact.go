// Package redact removes sensitive content that was shared by mistake: a file
// attached to a message, or a message's text. Agents may redact their own
// messages; admins may redact any message, including customer messages.
//
// encatch: our own package. Redaction removes the stored copy from libredesk
// (file, thumbnail, database row, text) right away and records who did it in a
// private note. It cannot recall emails that were already delivered, and
// backups keep older copies until they expire, so a leaked secret must still
// be rotated.
package redact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/abhinavxd/libredesk/internal/attachment"
	"github.com/abhinavxd/libredesk/internal/conversation"
	cmodels "github.com/abhinavxd/libredesk/internal/conversation/models"
	"github.com/abhinavxd/libredesk/internal/image"
	"github.com/abhinavxd/libredesk/internal/media"
	"github.com/jmoiron/sqlx"
	"github.com/zerodha/logf"
)

// RemovedText replaces a message's text after redaction.
const RemovedText = "[Removed for security]"

// Reasons are required: at least MinReasonWords words, at most MaxReasonLen characters.
const (
	MinReasonWords = 5
	MaxReasonLen   = 500
)

var (
	ErrNotFound   = errors.New("message or attachment not found")
	ErrForbidden  = errors.New("you can only remove content from your own messages")
	ErrNotAllowed = errors.New("this kind of message can't be edited")
	ErrReason     = fmt.Errorf("please explain why in at least %d words", MinReasonWords)
)

// CheckReason validates the reason an agent gives, and returns it trimmed.
func CheckReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if len(strings.Fields(reason)) < MinReasonWords || len([]rune(reason)) > MaxReasonLen {
		return "", ErrReason
	}
	return reason, nil
}

// schema is our own table (encatch_ prefix, not an upstream migration). It has
// no foreign keys so the log outlives deleted tickets and agents.
const schema = `
CREATE TABLE IF NOT EXISTS encatch_redactions (
	id BIGSERIAL PRIMARY KEY,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	conversation_uuid UUID NOT NULL,
	conversation_reference TEXT NOT NULL DEFAULT '',
	message_uuid UUID NOT NULL,
	message_type TEXT NOT NULL,
	message_private BOOLEAN NOT NULL,
	message_sender_type TEXT NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('attachment', 'text')),
	file_name TEXT NULL,
	exposure TEXT NOT NULL DEFAULT '',
	actor_id INT NOT NULL,
	actor_name TEXT NOT NULL,
	actor_is_admin BOOLEAN NOT NULL,
	reason TEXT NOT NULL,
	outcome TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX IF NOT EXISTS encatch_redactions_conversation ON encatch_redactions (conversation_uuid);
CREATE INDEX IF NOT EXISTS encatch_redactions_created ON encatch_redactions (created_at);`

// EnsureSchema creates the log table if needed. It is idempotent and runs at startup.
func EnsureSchema(db *sqlx.DB) error {
	_, err := db.Exec(schema)
	return err
}

// Actor is the agent doing the redaction.
type Actor struct {
	ID      int
	Name    string
	IsAdmin bool
}

// Msg is the part of a message the rules need.
type Msg struct {
	ID             int       `db:"id"`
	UUID           string    `db:"uuid"`
	Type           string    `db:"type"`
	Status         string    `db:"status"`
	Private        bool      `db:"private"`
	SenderID       int       `db:"sender_id"`
	SenderType     string    `db:"sender_type"`
	ConversationID int       `db:"conversation_id"`
	CreatedAt      time.Time `db:"created_at"`
	Reference      string    `db:"reference_number"`
}

// Allowed reports whether actor may redact m: incoming and outgoing messages
// only (not activity or system rows); agents their own, admins any.
func Allowed(a Actor, m Msg) error {
	if m.Type != cmodels.MessageIncoming && m.Type != cmodels.MessageOutgoing {
		return ErrNotAllowed
	}
	if a.IsAdmin {
		return nil
	}
	if m.SenderType == "agent" && m.SenderID == a.ID {
		return nil
	}
	return ErrForbidden
}

// Exposure says where else the content may still exist, for the agent's warning.
func Exposure(m Msg) string {
	switch {
	case m.Private:
		return "" // internal note: only ever stored in libredesk
	case m.Type == cmodels.MessageIncoming:
		return "from_customer" // the customer's own sent copy remains
	case m.Status == "sent":
		return "emailed" // already delivered to the customer's inbox
	default:
		return "" // pending or failed: not delivered
	}
}

// Result is returned to the agent app to update the message in place.
type Result struct {
	Content     string                 `json:"content,omitempty"`
	TextContent string                 `json:"text_content,omitempty"`
	ContentType string                 `json:"content_type,omitempty"`
	Attachments attachment.Attachments `json:"attachments"`
	Meta        json.RawMessage        `json:"meta"`
	Exposure    string                 `json:"exposure"`
}

// Service does the redaction.
type Service struct {
	DB            *sqlx.DB
	Conversations *conversation.Manager
	Media         *media.Manager
	Lo            *logf.Logger
}

const getMsgSQL = `
SELECT m.id, m.uuid, m.type, m.status, m.private, m.sender_id, m.sender_type, m.conversation_id, m.created_at,
       c.reference_number
FROM conversation_messages m JOIN conversations c ON c.id = m.conversation_id
WHERE m.uuid = $1 AND c.uuid = $2`

// logStart records a redaction before it runs, so even a failed attempt is logged.
func (s *Service) logStart(a Actor, convUUID string, m Msg, kind, fileName, reason string) (int64, error) {
	var id int64
	var file any
	if fileName != "" {
		file = fileName
	}
	err := s.DB.Get(&id, `INSERT INTO encatch_redactions (conversation_uuid, conversation_reference, message_uuid, message_type,
		message_private, message_sender_type, kind, file_name, exposure, actor_id, actor_name, actor_is_admin, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		convUUID, m.Reference, m.UUID, m.Type, m.Private, m.SenderType, kind, file, Exposure(m), a.ID, a.Name, a.IsAdmin, reason)
	if err != nil {
		return 0, fmt.Errorf("logging removal: %w", err)
	}
	return id, nil
}

func (s *Service) logEnd(id int64, err error) {
	outcome := "done"
	if err != nil {
		outcome = "failed: " + err.Error()
	}
	if _, e := s.DB.Exec(`UPDATE encatch_redactions SET outcome = $2 WHERE id = $1`, id, outcome); e != nil {
		s.Lo.Error("redact: updating log", "id", id, "error", e)
	}
}

func (s *Service) load(a Actor, convUUID, msgUUID string) (Msg, error) {
	var m Msg
	if err := s.DB.Get(&m, getMsgSQL, msgUUID, convUUID); err != nil {
		return m, ErrNotFound
	}
	return m, Allowed(a, m)
}

// RemoveAttachment deletes one attachment of a message.
func (s *Service) RemoveAttachment(a Actor, convUUID, msgUUID, mediaUUID, reason string) (res Result, err error) {
	m, err := s.load(a, convUUID, msgUUID)
	if err != nil {
		return Result{}, err
	}
	if reason, err = CheckReason(reason); err != nil {
		return Result{}, err
	}
	var name string
	if err := s.DB.Get(&name, `SELECT filename FROM media WHERE uuid = $1 AND model_type = 'messages' AND model_id = $2`, mediaUUID, m.ID); err != nil {
		return Result{}, ErrNotFound
	}
	logID, err := s.logStart(a, convUUID, m, "attachment", name, reason)
	if err != nil {
		return Result{}, err
	}
	defer func() { s.logEnd(logID, err) }()
	if err := s.deleteMedia(mediaUUID); err != nil {
		return Result{}, err
	}
	if err := s.mark(m.ID, "encatch_removed_attachments", map[string]any{"name": name, "by": a.ID, "at": time.Now()}); err != nil {
		return Result{}, err
	}
	s.note(a, convUUID, fmt.Sprintf("Removed the attachment <strong>%s</strong> from %s.", safe(name), whose(a, m)), reason, Exposure(m))
	s.Lo.Info("redact: attachment removed", "conversation", convUUID, "message", msgUUID, "file", name, "by", a.ID)
	return s.result(convUUID, msgUUID, m, false)
}

// RemoveText replaces a message's text, and deletes images inline in it.
func (s *Service) RemoveText(a Actor, convUUID, msgUUID, reason string) (res Result, err error) {
	m, err := s.load(a, convUUID, msgUUID)
	if err != nil {
		return Result{}, err
	}
	if reason, err = CheckReason(reason); err != nil {
		return Result{}, err
	}
	logID, err := s.logStart(a, convUUID, m, "text", "", reason)
	if err != nil {
		return Result{}, err
	}
	defer func() { s.logEnd(logID, err) }()
	var inline []string
	if err := s.DB.Select(&inline, `SELECT uuid FROM media WHERE model_type = 'messages' AND model_id = $1 AND disposition = $2`, m.ID, attachment.DispositionInline); err != nil {
		return Result{}, fmt.Errorf("listing inline images: %w", err)
	}
	for _, id := range inline {
		if err := s.deleteMedia(id); err != nil {
			return Result{}, err
		}
	}
	marker, _ := json.Marshal(map[string]any{"encatch_text_removed": map[string]any{"by": a.ID, "at": time.Now()}})
	tx, err := s.DB.Beginx()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	// Plain text: the placeholder has no markup, and the agent app re-renders
	// plain-text messages in place (its HTML renderer doesn't).
	if _, err := tx.Exec(`UPDATE conversation_messages SET content = $2, text_content = $2, content_type = 'text', updated_at = NOW(),
		meta = COALESCE(meta, '{}'::jsonb) || $3::jsonb WHERE id = $1`, m.ID, RemovedText, string(marker)); err != nil {
		return Result{}, fmt.Errorf("removing text: %w", err)
	}
	// The inbox list shows the latest message and interaction; replace them too
	// when this message is the latest.
	var latest, latestInteraction bool
	if err := tx.Get(&latest, `SELECT NOT EXISTS (SELECT 1 FROM conversation_messages WHERE conversation_id = $1 AND created_at > $2)`, m.ConversationID, m.CreatedAt); err != nil {
		return Result{}, err
	}
	if latest {
		if _, err := tx.Exec(`UPDATE conversations SET last_message = $2 WHERE id = $1`, m.ConversationID, RemovedText); err != nil {
			return Result{}, err
		}
	}
	if !m.Private {
		if err := tx.Get(&latestInteraction, `SELECT NOT EXISTS (SELECT 1 FROM conversation_messages WHERE conversation_id = $1 AND created_at > $2
			AND private = false AND type IN ('incoming', 'outgoing'))`, m.ConversationID, m.CreatedAt); err != nil {
			return Result{}, err
		}
		if latestInteraction {
			if _, err := tx.Exec(`UPDATE conversations SET last_interaction = $2 WHERE id = $1`, m.ConversationID, RemovedText); err != nil {
				return Result{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	if latest {
		s.Conversations.BroadcastConversationUpdate(convUUID, map[string]any{"last_message": RemovedText})
	}
	s.note(a, convUUID, fmt.Sprintf("Removed the text of %s.", whose(a, m)), reason, Exposure(m))
	s.Lo.Info("redact: text removed", "conversation", convUUID, "message", msgUUID, "by", a.ID)
	return s.result(convUUID, msgUUID, m, true)
}

// result reloads the message (attachment URLs signed) and pushes the change to
// agents viewing the conversation.
func (s *Service) result(convUUID, msgUUID string, m Msg, withText bool) (Result, error) {
	fresh, err := s.Conversations.GetMessage(msgUUID)
	if err != nil {
		return Result{}, fmt.Errorf("reloading message: %w", err)
	}
	if fresh.Attachments == nil {
		fresh.Attachments = attachment.Attachments{}
	}
	r := Result{Attachments: fresh.Attachments, Meta: fresh.Meta, Exposure: Exposure(m)}
	update := map[string]any{"attachments": r.Attachments, "meta": r.Meta}
	if withText {
		r.Content, r.TextContent, r.ContentType = fresh.Content, fresh.TextContent, fresh.ContentType
		update["content"], update["text_content"], update["content_type"] = r.Content, r.TextContent, r.ContentType
	}
	s.Conversations.BroadcastMessageUpdate(convUUID, msgUUID, update)
	return r, nil
}

// mark appends an entry to a list in the message's meta, as a record.
func (s *Service) mark(msgID int, key string, entry map[string]any) error {
	b, _ := json.Marshal([]any{entry})
	_, err := s.DB.Exec(`UPDATE conversation_messages SET updated_at = NOW(),
		meta = jsonb_set(COALESCE(meta, '{}'::jsonb), ARRAY[$2], COALESCE(meta->$2, '[]'::jsonb) || $3::jsonb)
		WHERE id = $1`, msgID, key, string(b))
	return err
}

// deleteMedia removes a file, its thumbnail and its row now. Existing signed
// links stop working because the file is gone.
func (s *Service) deleteMedia(id string) error {
	if err := s.Media.Delete(id); err != nil {
		return fmt.Errorf("deleting file: %w", err)
	}
	_ = s.Media.Delete(image.ThumbPrefix + id) // only images have one
	return nil
}

// note records the redaction for agents. A failure here is logged, not fatal:
// the content is already gone.
func (s *Service) note(a Actor, convUUID, what, reason, exposure string) {
	var b strings.Builder
	fmt.Fprintf(&b, "<p>🔒 %s</p>", what)
	fmt.Fprintf(&b, "<p>Reason: %s</p>", safe(reason))
	switch exposure {
	case "emailed":
		b.WriteString("<p>It had already been emailed to the customer, so their copy remains. Rotate any exposed password or key.</p>")
	case "from_customer":
		b.WriteString("<p>The customer's own sent copy remains. Ask them to rotate any exposed password or key.</p>")
	}
	if _, err := s.Conversations.SendPrivateNote(nil, a.ID, convUUID, b.String(), nil); err != nil {
		s.Lo.Error("redact: recording note", "conversation", convUUID, "error", err)
	}
}

// safe escapes user-supplied text (file names, reasons) for the note. Braces
// become entities too: SendPrivateNote renders Go template syntax, so "{{"
// in a file name must never reach it.
func safe(s string) string {
	s = html.EscapeString(s)
	return strings.NewReplacer("{", "&#123;", "}", "&#125;").Replace(s)
}

// whose names the redacted message in the note, which is posted as the actor.
func whose(a Actor, m Msg) string {
	switch {
	case m.SenderType == "agent" && m.SenderID == a.ID:
		if m.Private {
			return "my note"
		}
		return "my reply"
	case m.Type == cmodels.MessageIncoming:
		return "the customer's message"
	case m.Private:
		return "an agent's note"
	default:
		return "an agent's reply"
	}
}

// PurgeDeletedNoteFiles removes files of deleted private notes right away.
// Libredesk unlinks them (model_id = 0) and would otherwise keep them for 7
// days; model_id = 0 is set only by its delete-private-message query.
func (s *Service) PurgeDeletedNoteFiles(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		var ids []string
		if err := s.DB.SelectContext(ctx, &ids, `SELECT uuid FROM media WHERE model_type = 'messages' AND model_id = 0`); err != nil && ctx.Err() == nil {
			s.Lo.Error("redact: listing deleted note files", "error", err)
		}
		for _, id := range ids {
			if err := s.deleteMedia(id); err != nil {
				s.Lo.Error("redact: purging deleted note file", "uuid", id, "error", err)
				continue
			}
			s.Lo.Info("redact: purged file of a deleted note", "uuid", id)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
