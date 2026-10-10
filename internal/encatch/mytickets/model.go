package mytickets

import (
	"strings"
	"time"
)

// Customer-facing statuses.
const (
	StatusOpen       = "Open"
	StatusWaitingYou = "Waiting on you"
	StatusResolved   = "Resolved"
)

// CustomerStatus maps an internal libredesk status name to what customers see.
// Internal states such as "Waiting on engineering" stay hidden as "Open".
func CustomerStatus(internal string) string {
	switch internal {
	case "Waiting on customer":
		return StatusWaitingYou
	case "Resolved", "Closed":
		return StatusResolved
	default:
		return StatusOpen
	}
}

// Ticket custom attribute keys (defined in deploy/helpdesk-config).
const (
	AttrOrgID             = "org_id"
	AttrOrgName           = "org_name"
	AttrTicketTier        = "ticket_tier"
	AttrRequestedPriority = "requested_priority"
	AttrProjectID         = "project_id"
	AttrProjectName       = "project_name"
	AttrSourceApp         = "source_app"
)

// Requested priority choices on the new-ticket form.
var PriorityChoices = []string{"Normal", "Express", "Urgent"}

// Session is what a signed-in customer may do, captured from the token at sign-in.
type Session struct {
	ContactID        int       `json:"contact_id"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	Issuer           string    `json:"iss"`
	OrgID            string    `json:"org_id"`
	OrgName          string    `json:"org_name"`
	SupportTier      string    `json:"support_tier"`
	Scope            string    `json:"scope"`
	Projects         []Project `json:"projects"`
	CurrentProjectID string    `json:"current_project_id"`
	CSRF             string    `json:"csrf"`
	CreatedAt        time.Time `json:"created_at"`
}

// ProjectIDs returns the IDs of the projects in the session.
func (s Session) ProjectIDs() []string {
	ids := make([]string, 0, len(s.Projects))
	for _, p := range s.Projects {
		ids = append(ids, string(p.ID))
	}
	return ids
}

// ProjectName returns the name of a project the session can see, and whether it can.
func (s Session) ProjectName(id string) (string, bool) {
	for _, p := range s.Projects {
		if string(p.ID) == id {
			return p.Name, true
		}
	}
	return "", false
}

// TicketSummary is one row of the ticket list.
type TicketSummary struct {
	UUID            string
	ReferenceNumber string
	Subject         string
	InternalStatus  string
	ContactID       int
	RaisedBy        string
	OrgID           string
	ProjectID       string
	ProjectName     string
	CreatedAt       time.Time
	UpdatedAt       time.Time // time of the latest customer-visible message

	// Latest customer-visible message, for the list preview. Never private notes.
	LastFromCustomer bool
	LastAuthorID     int
	LastAuthor       string
	Preview          string
}

// maxPreview is the longest list preview, in runes.
const maxPreview = 240

// Clip collapses whitespace and shortens s for a list preview.
func Clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxPreview {
		s = strings.TrimSpace(string(r[:maxPreview])) + "…"
	}
	return s
}

// Message is a customer-visible message in a ticket thread.
type Message struct {
	FromCustomer bool
	AuthorName   string
	HTML         string
	CreatedAt    time.Time
	Attachments  []Attachment
}

// Attachment is a file on a message, with a signed download URL.
type Attachment struct {
	Name string
	URL  string
}

// ListQuery selects the tickets a session may see.
type ListQuery struct {
	Scope      string
	ContactID  int
	OrgID      string
	ProjectIDs []string
	Status     string // optional customer-facing status filter
	ProjectID  string // optional project filter
	Search     string // optional: subject text or ticket number
	Limit      int
}

// Visible reports whether a session may see a ticket. It mirrors the SQL in the
// adapter and is used for single-ticket access checks.
func Visible(s Session, t TicketSummary) bool {
	switch s.Scope {
	case ScopeOrg:
		return t.OrgID == s.OrgID
	case ScopeProject:
		if t.OrgID != s.OrgID {
			return false
		}
		_, ok := s.ProjectName(t.ProjectID)
		return ok && t.ProjectID != ""
	default: // self: own tickets for this org, plus own tickets with no org (email)
		return t.ContactID == s.ContactID && (t.OrgID == s.OrgID || t.OrgID == "")
	}
}

// PriorityEligible reports whether a tier may choose Express/Urgent.
func PriorityEligible(tier string, eligible []string) bool {
	return contains(eligible, tier)
}

// Backend is everything My Tickets needs from libredesk. adapter.go implements it on
// top of libredesk's managers; tests use a fake.
type Backend interface {
	// ResolveContact finds or creates the contact for a person and returns its ID.
	ResolveContact(externalUserID, email, firstName, lastName string) (int, error)
	// ListTickets returns the tickets matching q, newest activity first.
	ListTickets(q ListQuery) ([]TicketSummary, error)
	// GetTicket loads one ticket by reference number.
	GetTicket(referenceNumber string) (TicketSummary, error)
	// Messages returns the customer-visible messages of a ticket, oldest first.
	Messages(uuid string) ([]Message, error)
	// CreateTicket creates a contact-initiated ticket and returns its reference number.
	CreateTicket(contactID int, subject, html string, attrs map[string]any) (string, error)
	// AddReply adds a customer reply to a ticket.
	AddReply(contactID int, uuid, html string) error
}
