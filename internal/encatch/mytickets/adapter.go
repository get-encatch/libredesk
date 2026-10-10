package mytickets

// encatch: the ONLY file in this package that calls libredesk code. When an upstream
// merge changes a signature used here, the build fails in this file and nowhere else.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/abhinavxd/libredesk/internal/attachment"
	"github.com/abhinavxd/libredesk/internal/conversation"
	cmodels "github.com/abhinavxd/libredesk/internal/conversation/models"
	"github.com/abhinavxd/libredesk/internal/image"
	"github.com/abhinavxd/libredesk/internal/media"
	mmodels "github.com/abhinavxd/libredesk/internal/media/models"
	"github.com/abhinavxd/libredesk/internal/stringutil"
	"github.com/abhinavxd/libredesk/internal/user"
	umodels "github.com/abhinavxd/libredesk/internal/user/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/volatiletech/null/v9"
)

// LibredeskBackend implements Backend on top of libredesk's managers.
type LibredeskBackend struct {
	Users         *user.Manager
	Conversations *conversation.Manager
	Media         *media.Manager
	DB            *sqlx.DB
	InboxID       int
}

func (b *LibredeskBackend) ResolveContact(externalUserID, email, firstName, lastName string) (int, error) {
	c := umodels.User{
		Email:            null.StringFrom(email),
		FirstName:        firstName,
		LastName:         lastName,
		ExternalUserID:   null.NewString(externalUserID, externalUserID != ""),
		CustomAttributes: json.RawMessage(`{}`),
	}
	// Sync: the token is authoritative for the person's name, email and user ID.
	if err := b.Users.ResolveContact(&c, umodels.ContactSync); err != nil {
		return 0, fmt.Errorf("resolving contact: %w", err)
	}
	return c.ID, nil
}

// listSQL selects ticket summaries; the WHERE clause is built from the scope.
const listSQL = `
SELECT c.uuid, c.reference_number, COALESCE(c.subject, '') AS subject, s.name AS status,
       c.contact_id, TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS raised_by,
       COALESCE(c.custom_attributes->>'org_id', '') AS org_id,
       COALESCE(c.custom_attributes->>'project_id', '') AS project_id,
       COALESCE(c.custom_attributes->>'project_name', '') AS project_name,
       c.created_at, COALESCE(lm.created_at, c.created_at) AS updated_at,
       COALESCE(lm.from_customer, false) AS last_from_customer, COALESCE(lm.sender_id, 0) AS last_author_id,
       COALESCE(lm.sender_name, '') AS last_author, COALESCE(lm.preview, '') AS preview
FROM conversations c
JOIN conversation_statuses s ON s.id = c.status_id
JOIN users u ON u.id = c.contact_id
-- The latest customer-visible message, with the same filter as the ticket page.
-- (conversations.last_message and last_message_at also count private notes.)
LEFT JOIN LATERAL (
    SELECT m.created_at, m.type = 'incoming' AS from_customer, m.sender_id,
           TRIM(COALESCE(mu.first_name, '') || ' ' || COALESCE(mu.last_name, '')) AS sender_name,
           LEFT(COALESCE(m.text_content, ''), 1000) AS preview
    FROM conversation_messages m
    LEFT JOIN users mu ON mu.id = m.sender_id
    WHERE m.conversation_id = c.id AND m.private = false AND m.type IN ('incoming', 'outgoing')
      AND (m.meta IS NULL OR NOT COALESCE((m.meta->>'continuity_email')::boolean, false))
    ORDER BY m.created_at DESC
    LIMIT 1
) lm ON true
WHERE %s
ORDER BY updated_at DESC
LIMIT %d`

type listRow struct {
	UUID             string    `db:"uuid"`
	ReferenceNumber  string    `db:"reference_number"`
	Subject          string    `db:"subject"`
	Status           string    `db:"status"`
	ContactID        int       `db:"contact_id"`
	RaisedBy         string    `db:"raised_by"`
	OrgID            string    `db:"org_id"`
	ProjectID        string    `db:"project_id"`
	ProjectName      string    `db:"project_name"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
	LastFromCustomer bool      `db:"last_from_customer"`
	LastAuthorID     int       `db:"last_author_id"`
	LastAuthor       string    `db:"last_author"`
	Preview          string    `db:"preview"`
}

// scopeWhere builds the WHERE clause and args for a list query. It must match Visible.
// likeEscaper escapes ILIKE wildcards in search text (backslash is the default escape).
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func scopeWhere(q ListQuery) (string, []any) {
	var (
		conds []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	switch q.Scope {
	case ScopeOrg:
		conds = append(conds, "c.custom_attributes->>'org_id' = "+arg(q.OrgID))
	case ScopeProject:
		conds = append(conds,
			"c.custom_attributes->>'org_id' = "+arg(q.OrgID),
			"c.custom_attributes->>'project_id' = ANY("+arg(pq.Array(q.ProjectIDs))+")")
	default:
		conds = append(conds,
			"c.contact_id = "+arg(q.ContactID),
			"(c.custom_attributes->>'org_id' = "+arg(q.OrgID)+" OR NOT (c.custom_attributes ? 'org_id'))")
	}
	if q.ProjectID != "" {
		conds = append(conds, "c.custom_attributes->>'project_id' = "+arg(q.ProjectID))
	}
	if q.Search != "" {
		like := "%" + likeEscaper.Replace(q.Search) + "%"
		if ref := strings.TrimPrefix(q.Search, "#"); isDigits(ref) {
			conds = append(conds, "(c.reference_number = "+arg(ref)+" OR c.subject ILIKE "+arg(like)+")")
		} else {
			conds = append(conds, "c.subject ILIKE "+arg(like))
		}
	}
	switch q.Status {
	case StatusWaitingYou:
		conds = append(conds, "s.name = 'Waiting on customer'")
	case StatusResolved:
		conds = append(conds, "s.name IN ('Resolved', 'Closed')")
	case StatusOpen:
		conds = append(conds, "s.name NOT IN ('Waiting on customer', 'Resolved', 'Closed')")
	}
	return strings.Join(conds, " AND "), args
}

func (b *LibredeskBackend) ListTickets(q ListQuery) ([]TicketSummary, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	where, args := scopeWhere(q)
	var rows []listRow
	if err := b.DB.Select(&rows, fmt.Sprintf(listSQL, where, limit), args...); err != nil {
		return nil, fmt.Errorf("listing tickets: %w", err)
	}
	out := make([]TicketSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, TicketSummary{
			UUID: r.UUID, ReferenceNumber: r.ReferenceNumber, Subject: r.Subject, InternalStatus: r.Status,
			ContactID: r.ContactID, RaisedBy: r.RaisedBy, OrgID: r.OrgID, ProjectID: r.ProjectID,
			ProjectName: r.ProjectName, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			LastFromCustomer: r.LastFromCustomer, LastAuthorID: r.LastAuthorID, LastAuthor: r.LastAuthor,
			Preview: Clip(r.Preview),
		})
	}
	return out, nil
}

func (b *LibredeskBackend) GetTicket(referenceNumber string) (TicketSummary, error) {
	c, err := b.Conversations.GetConversation(0, "", referenceNumber)
	if err != nil {
		return TicketSummary{}, fmt.Errorf("loading ticket: %w", err)
	}
	attrs := map[string]any{}
	if len(c.CustomAttributes) > 0 {
		_ = json.Unmarshal(c.CustomAttributes, &attrs)
	}
	updated := c.CreatedAt
	if c.LastMessageAt.Valid {
		updated = c.LastMessageAt.Time
	}
	return TicketSummary{
		UUID: c.UUID, ReferenceNumber: c.ReferenceNumber, Subject: c.Subject.String, InternalStatus: c.Status.String,
		ContactID: c.ContactID, RaisedBy: strings.TrimSpace(c.Contact.FirstName + " " + c.Contact.LastName),
		OrgID: attrString(attrs, AttrOrgID), ProjectID: attrString(attrs, AttrProjectID),
		ProjectName: attrString(attrs, AttrProjectName), CreatedAt: c.CreatedAt, UpdatedAt: updated,
	}, nil
}

func (b *LibredeskBackend) Messages(uuid string) ([]Message, error) {
	private := false
	msgs, _, err := b.Conversations.GetConversationMessages(uuid, 1, 200,
		&private, []string{cmodels.MessageIncoming, cmodels.MessageOutgoing})
	if err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Private || (m.Type != cmodels.MessageIncoming && m.Type != cmodels.MessageOutgoing) {
			continue
		}
		// The message list has no attachment URLs; sign them as the agent API does
		// (cmd/messages.go), and point inline cid: images at the same signed URLs.
		b.Conversations.SignAttachmentURLs(m.Attachments)
		content := m.Content
		for _, a := range m.Attachments {
			if a.ContentID != "" && a.URL != "" {
				content = strings.ReplaceAll(content, "cid:"+a.ContentID, relativeURL(a.URL))
			}
		}
		if b.Media != nil {
			if refs, err := b.Conversations.GetInlineMediaRefs(&m); err == nil { // images quoted from earlier messages
				for _, ref := range refs {
					content = strings.ReplaceAll(content, "cid:"+ref.ContentID, relativeURL(b.Media.GetURL(ref.UUID, ref.ContentType, ref.Filename)))
				}
			}
		}
		msg := Message{
			FromCustomer: m.Type == cmodels.MessageIncoming,
			AuthorName:   strings.TrimSpace(m.Author.FirstName + " " + m.Author.LastName),
			HTML:         content,
			CreatedAt:    m.CreatedAt,
		}
		for _, a := range m.Attachments {
			if a.URL == "" || (a.Disposition == attachment.DispositionInline && a.ContentID != "" && strings.Contains(m.Content, "cid:"+a.ContentID)) {
				continue // inline images already show in the message body
			}
			msg.Attachments = append(msg.Attachments, Attachment{Name: a.Name, URL: relativeURL(a.URL)})
		}
		out = append(out, msg)
	}
	// Libredesk returns newest first; threads read oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (b *LibredeskBackend) CreateTicket(contactID int, subject, html string, attrs map[string]any, files []Upload) (string, error) {
	// Store files first, so a failed upload doesn't leave a ticket behind.
	stored, err := b.storeUploads(files)
	if err != nil {
		return "", err
	}
	_, convUUID, err := b.Conversations.CreateConversation(contactID, b.InboxID, "", time.Now(), subject,
		true /* append reference number to subject */, nil, attrs, 0, 0)
	if err != nil {
		b.deleteUploads(stored)
		return "", fmt.Errorf("creating ticket: %w", err)
	}
	// A contact message on a new conversation runs the same hooks as an incoming email:
	// new-ticket automation rules (tier SLA, team assignment) and SLA tracking.
	if _, err := b.Conversations.CreateContactMessage(stored, contactID, convUUID, html, cmodels.ContentTypeHTML, true); err != nil {
		_ = b.Conversations.DeleteConversation(convUUID)
		b.deleteUploads(stored)
		return "", fmt.Errorf("creating first message: %w", err)
	}
	c, err := b.Conversations.GetConversation(0, convUUID, "")
	if err != nil {
		return "", fmt.Errorf("loading new ticket: %w", err)
	}
	return c.ReferenceNumber, nil
}

func (b *LibredeskBackend) AddReply(contactID int, convUUID, html string, files []Upload) error {
	stored, err := b.storeUploads(files)
	if err != nil {
		return err
	}
	if _, err := b.Conversations.CreateContactMessage(stored, contactID, convUUID, html, cmodels.ContentTypeHTML, false); err != nil {
		b.deleteUploads(stored)
		return fmt.Errorf("adding reply: %w", err)
	}
	return nil
}

// storeUploads saves customer files the way the agent app's media upload does
// (cmd/media.go): private media rows named by UUID, with a thumbnail for images.
// CreateContactMessage then links them to the message.
func (b *LibredeskBackend) storeUploads(files []Upload) ([]mmodels.Media, error) {
	stored := make([]mmodels.Media, 0, len(files))
	for _, f := range files {
		m, err := b.storeUpload(f)
		if err != nil {
			b.deleteUploads(stored)
			return nil, err
		}
		stored = append(stored, m)
	}
	return stored, nil
}

func (b *LibredeskBackend) storeUpload(f Upload) (mmodels.Media, error) {
	if b.Media == nil {
		return mmodels.Media{}, fmt.Errorf("attachments are not configured")
	}
	name := stringutil.SanitizeFilename(f.Name)
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	id := uuid.New().String()
	thumb := image.ThumbPrefix + id

	meta := []byte("{}")
	isImage := slices.Contains(image.Exts, ext) && image.IsImageByContent(bytes.NewReader(f.Data))
	if isImage {
		t, err := image.CreateThumb(image.DefThumbSize, bytes.NewReader(f.Data))
		if err != nil {
			return mmodels.Media{}, fmt.Errorf("creating thumbnail: %w", err)
		}
		if _, _, err := b.Media.Upload(thumb, f.ContentType, t); err != nil {
			return mmodels.Media{}, fmt.Errorf("uploading thumbnail: %w", err)
		}
		if w, h, err := image.GetDimensions(bytes.NewReader(f.Data)); err == nil {
			meta, _ = json.Marshal(map[string]int{"width": w, "height": h})
		}
	}
	// Upload detects the real content type from the bytes.
	_, contentType, err := b.Media.Upload(id, f.ContentType, bytes.NewReader(f.Data))
	if err != nil {
		if isImage {
			_ = b.Media.Delete(thumb)
		}
		return mmodels.Media{}, fmt.Errorf("uploading attachment: %w", err)
	}
	m, err := b.Media.Insert(null.StringFrom(attachment.DispositionAttachment), name, contentType, "",
		null.String{}, id, null.Int{}, len(f.Data), meta, true /* private: signed links only */)
	if err != nil {
		_ = b.Media.Delete(id)
		if isImage {
			_ = b.Media.Delete(thumb)
		}
		return mmodels.Media{}, fmt.Errorf("saving attachment: %w", err)
	}
	return m, nil
}

// deleteUploads removes stored files after a failure. Libredesk also cleans up
// media that never got linked to a message.
func (b *LibredeskBackend) deleteUploads(stored []mmodels.Media) {
	for _, m := range stored {
		_ = b.Media.Delete(m.UUID)
		_ = b.Media.Delete(image.ThumbPrefix + m.UUID)
	}
}

func attrString(attrs map[string]any, key string) string {
	switch v := attrs[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// relativeURL turns an absolute media URL (built from the agents' Root URL) into a
// path, so customers download through support.encatch.com.
func relativeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return raw
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}
