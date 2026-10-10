package mytickets

// encatch: the ONLY file in this package that calls libredesk code. When an upstream
// merge changes a signature used here, the build fails in this file and nowhere else.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/abhinavxd/libredesk/internal/conversation"
	cmodels "github.com/abhinavxd/libredesk/internal/conversation/models"
	"github.com/abhinavxd/libredesk/internal/user"
	umodels "github.com/abhinavxd/libredesk/internal/user/models"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/volatiletech/null/v9"
)

// LibredeskBackend implements Backend on top of libredesk's managers.
type LibredeskBackend struct {
	Users         *user.Manager
	Conversations *conversation.Manager
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
		msg := Message{
			FromCustomer: m.Type == cmodels.MessageIncoming,
			AuthorName:   strings.TrimSpace(m.Author.FirstName + " " + m.Author.LastName),
			HTML:         m.Content,
			CreatedAt:    m.CreatedAt,
		}
		for _, a := range m.Attachments {
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

func (b *LibredeskBackend) CreateTicket(contactID int, subject, html string, attrs map[string]any) (string, error) {
	_, uuid, err := b.Conversations.CreateConversation(contactID, b.InboxID, "", time.Now(), subject,
		true /* append reference number to subject */, nil, attrs, 0, 0)
	if err != nil {
		return "", fmt.Errorf("creating ticket: %w", err)
	}
	// A contact message on a new conversation runs the same hooks as an incoming email:
	// new-ticket automation rules (tier SLA, team assignment) and SLA tracking.
	if _, err := b.Conversations.CreateContactMessage(nil, contactID, uuid, html, cmodels.ContentTypeHTML, true); err != nil {
		_ = b.Conversations.DeleteConversation(uuid)
		return "", fmt.Errorf("creating first message: %w", err)
	}
	c, err := b.Conversations.GetConversation(0, uuid, "")
	if err != nil {
		return "", fmt.Errorf("loading new ticket: %w", err)
	}
	return c.ReferenceNumber, nil
}

func (b *LibredeskBackend) AddReply(contactID int, uuid, html string) error {
	if _, err := b.Conversations.CreateContactMessage(nil, contactID, uuid, html, cmodels.ContentTypeHTML, false); err != nil {
		return fmt.Errorf("adding reply: %w", err)
	}
	return nil
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
