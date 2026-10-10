package mytickets

import (
	"slices"
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
	AttrInstance          = "encatch_instance" // which Encatch instance the ticket came from
)

// Requested priority choices on the new-ticket form.
var PriorityChoices = []string{"Normal", "Express", "Urgent"}

// Session is what a signed-in customer may do, captured from the token at sign-in.
// Orgs holds every org the token granted (ids instance-prefixed); the fields after it
// describe the current org, which the customer can switch (UseOrg).
type Session struct {
	ContactID int    `json:"contact_id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Issuer    string `json:"iss"`
	Instance  string `json:"instance"`
	Orgs      []Org  `json:"orgs"`

	OrgID       string    `json:"org_id"`
	OrgName     string    `json:"org_name"`
	OrgAccess   string    `json:"org_access"`   // manage | read: all the org's tickets; "": only Projects
	Scope       string    `json:"scope"`        // ScopeOrg or ScopeProject, from OrgAccess
	Projects    []Project `json:"projects"`     // the current org's projects with access
	SupportTier string    `json:"support_tier"` // the current org's tier, from the helpdesk (not the token)

	CSRF      string    `json:"csrf"`
	CreatedAt time.Time `json:"created_at"`
}

// UseOrg makes one of the session's orgs current. It reports false if the session
// doesn't have that org.
func (s *Session) UseOrg(id string) bool {
	for _, o := range s.Orgs {
		if string(o.ID) != id {
			continue
		}
		s.OrgID, s.OrgName, s.OrgAccess, s.Projects = string(o.ID), o.Name, o.Access, o.Projects
		s.Scope = ScopeProject
		if o.Access != "" {
			s.Scope = ScopeOrg
		}
		s.SupportTier = ""
		return true
	}
	return false
}

// CanManageOrg: raise org-level tickets and reply anywhere in the current org.
func (s Session) CanManageOrg() bool { return s.OrgAccess == AccessManage }

// CanManageProject: raise and reply in a project of the current org.
func (s Session) CanManageProject(id string) bool {
	if s.CanManageOrg() {
		return true
	}
	for _, p := range s.Projects {
		if string(p.ID) == id {
			return p.Access == AccessManage
		}
	}
	return false
}

// CanManageTicket: reply to (and attach files on) a visible ticket.
func (s Session) CanManageTicket(t TicketSummary) bool {
	if t.ProjectID == "" {
		return s.CanManageOrg() // org-level ticket
	}
	return s.CanManageProject(t.ProjectID)
}

// ManageProjects lists the current org's projects the user may raise tickets in.
func (s Session) ManageProjects() []Project {
	out := []Project{}
	for _, p := range s.Projects {
		if s.CanManageProject(string(p.ID)) {
			out = append(out, p)
		}
	}
	return out
}

// CanCreate: anything to raise a ticket in (org level or a project).
func (s Session) CanCreate() bool { return s.CanManageOrg() || len(s.ManageProjects()) > 0 }

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

// Upload is a file a customer attached to a new ticket or a reply. Handlers
// check it against the upload limits before the backend stores it.
type Upload struct {
	Name        string
	ContentType string
	Data        []byte
}

// UploadLimits are libredesk's upload settings (Admin > General), read per request.
type UploadLimits struct {
	MaxMB      int      // per file
	MaxTotalMB int      // all files of one ticket or reply together; 0 = no extra limit
	Extensions []string // lowercase, without dots; "*" allows any
}

// IntersectExtensions returns the file extensions allowed by both lists
// (lowercase, without dots). "*" in a list allows anything; an empty
// customer list means no extra restriction.
func IntersectExtensions(libredesk, customer []string) []string {
	norm := func(list []string) []string {
		out := make([]string, 0, len(list))
		for _, e := range list {
			if e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), ".")); e != "" {
				out = append(out, e)
			}
		}
		return out
	}
	ld, cu := norm(libredesk), norm(customer)
	switch {
	case len(cu) == 0 || slices.Contains(cu, "*"):
		return ld
	case slices.Contains(ld, "*"):
		return cu
	}
	var out []string
	for _, e := range cu {
		if slices.Contains(ld, e) {
			out = append(out, e)
		}
	}
	return out
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
	if t.OrgID == "" || t.OrgID != s.OrgID {
		return false
	}
	switch s.Scope {
	case ScopeOrg: // org access: every ticket in the org
		return true
	case ScopeProject: // project access only: their projects' tickets, never org-level ones
		_, ok := s.ProjectName(t.ProjectID)
		return ok && t.ProjectID != ""
	default:
		return false
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
	CreateTicket(contactID int, subject, html string, attrs map[string]any, files []Upload) (string, error)
	// RegisterOrg records that an org used My Tickets (keeping its name current) and
	// returns its support tier, which the helpdesk owns (see internal/encatch/orgtiers).
	RegisterOrg(instance, orgID, orgName string) (string, error)
	// LatestTicketOrg returns which of orgIDs the contact's most recent ticket belongs to
	// ("" if none), to open My Tickets there.
	LatestTicketOrg(contactID int, orgIDs []string) (string, error)
	// OrgTier returns an org's current support tier.
	OrgTier(orgID string) (string, error)
	// AddReply adds a customer reply to a ticket.
	AddReply(contactID int, uuid, html string, files []Upload) error
}
