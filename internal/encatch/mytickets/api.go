package mytickets

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// The JSON API under /my-tickets/api, for the Vue customer app. It applies the same
// rules as the server-rendered pages (and shares their helpers): the session cookie
// signs the customer in, and every POST needs the session's CSRF token in the
// X-CSRF-Token header. Errors are {"error": {"code", "message"}}, where message is
// safe to show the customer; successes are {"data": ...}.

const csrfHeader = "X-CSRF-Token"

// API error codes.
const (
	errSessionEnded = "session_ended" // 401: sign in again from Encatch
	errCSRF         = "csrf"          // 403: missing or wrong CSRF token
	errReadOnly     = "read_only"     // 403: the session may view but not do this
	errNotFound     = "not_found"     // 404
	errInvalid      = "invalid"       // 422: the message says what to fix
	errServer       = "server"        // 500
)

// APIRoute is one route of the JSON API, for the server (cmd/encatch_mytickets.go)
// and the local preview to register.
type APIRoute struct {
	Method, Path string
	Handler      func(*Service, *fastglue.Request) error
}

// Register adds the route to g with handler h (the route's Handler, wrapped).
func (rt APIRoute) Register(g *fastglue.Fastglue, h fastglue.FastRequestHandler) {
	if rt.Method == "POST" {
		g.POST(rt.Path, h)
	} else {
		g.GET(rt.Path, h)
	}
}

// APIRoutes lists the JSON API.
var APIRoutes = []APIRoute{
	{"GET", basePath + "/api/session", (*Service).APISession},
	{"POST", basePath + "/api/org", (*Service).APISwitchOrg},
	{"POST", basePath + "/api/logout", (*Service).APILogout},
	{"GET", basePath + "/api/tickets", (*Service).APIList},
	{"GET", basePath + "/api/new-ticket", (*Service).APINewTicket},
	{"POST", basePath + "/api/tickets", (*Service).APICreate},
	{"GET", basePath + "/api/tickets/{ref}", (*Service).APITicket},
	{"POST", basePath + "/api/tickets/{ref}/replies", (*Service).APIReply},
}

// ---- response shapes ----

type apiSession struct {
	User       apiUser       `json:"user"`
	Org        apiOrg        `json:"org"`
	Orgs       []apiOrgRef   `json:"orgs"`     // every org the user may switch to, current included
	Projects   []apiProject  `json:"projects"` // the current org's projects with access
	CanCreate  bool          `json:"can_create"`
	CSRF       string        `json:"csrf"`
	ExpiresAt  time.Time     `json:"expires_at"`
	SessionTTL string        `json:"session_ttl"` // "2 hours", for the session-ended message
	Uploads    *uploadView   `json:"uploads"`     // null: no attachments
	Limits     apiTextLimits `json:"limits"`
}

type apiUser struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Initials string `json:"initials"`
}

type apiOrg struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Access      string `json:"access"`       // manage | read: all the org's tickets; "": only projects
	AccessLabel string `json:"access_label"` // "All tickets · read-only", ...
}

type apiOrgRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Access string `json:"access"` // manage | read
}

type apiTextLimits struct {
	Subject int `json:"subject"`
	Message int `json:"message"`
	Search  int `json:"search"`
}

type apiTicketRow struct {
	Ref         string    `json:"ref"`
	Subject     string    `json:"subject"`
	Status      string    `json:"status"` // customer-facing: Open | Waiting on you | Resolved
	Sender      string    `json:"sender"` // who wrote the latest message: "You", "Encatch Support", a name
	FromSupport bool      `json:"from_support"`
	RaisedBy    string    `json:"raised_by"`
	ProjectID   string    `json:"project_id"`
	ProjectName string    `json:"project_name"`
	Preview     string    `json:"preview"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type apiList struct {
	Tickets      []apiTicketRow `json:"tickets"`
	Status       string         `json:"status"`
	Project      string         `json:"project"`
	ProjectName  string         `json:"project_name"`
	Search       string         `json:"search"`
	Page         int            `json:"page"`
	PerPage      int            `json:"per_page"`
	HasNewer     bool           `json:"has_newer"`
	HasOlder     bool           `json:"has_older"`
	ShowProject  bool           `json:"show_project"`
	ShowRaisedBy bool           `json:"show_raised_by"`
}

type apiTicket struct {
	Ref         string    `json:"ref"`
	Subject     string    `json:"subject"`
	Status      string    `json:"status"`
	ProjectID   string    `json:"project_id"`
	ProjectName string    `json:"project_name"`
	RaisedBy    string    `json:"raised_by"`
	RaisedByYou bool      `json:"raised_by_you"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type apiMessage struct {
	FromCustomer bool            `json:"from_customer"`
	Author       string          `json:"author"`
	Initials     string          `json:"initials"`
	HTML         string          `json:"html"` // sanitised
	CreatedAt    time.Time       `json:"created_at"`
	Attachments  []apiAttachment `json:"attachments"`
}

type apiAttachment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type apiThread struct {
	Page       int  `json:"page"` // 1 is the newest messages
	PerPage    int  `json:"per_page"`
	Total      int  `json:"total"`
	From       int  `json:"from"` // messages shown, counted oldest = 1
	To         int  `json:"to"`
	HasEarlier bool `json:"has_earlier"`
	HasNewer   bool `json:"has_newer"`
}

type apiTicketView struct {
	Ticket   apiTicket    `json:"ticket"`
	Messages []apiMessage `json:"messages"`
	Thread   apiThread    `json:"thread"`
	CanReply bool         `json:"can_reply"`
	// The session's current org: opening a ticket from another of the user's orgs
	// switches to it, and the app then reloads the session.
	OrgID string `json:"org_id"`
}

type apiNewTicket struct {
	Projects          []apiOrgRef `json:"projects"`        // projects the user may raise tickets in
	AllowOrgLevel     bool        `json:"allow_org_level"` // "Not about a specific project"
	DefaultProject    string      `json:"default_project"`
	CanChoosePriority bool        `json:"can_choose_priority"`
	Priorities        []string    `json:"priorities"`
	Uploads           *uploadView `json:"uploads"`
	Email             string      `json:"email"`
	OrgName           string      `json:"org_name"`
}

// ---- handlers ----

// APISession returns who is signed in, the current org and what they may do there.
func (s *Service) APISession(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, false)
	if !ok {
		return nil
	}
	return s.apiOK(r, fasthttp.StatusOK, s.sessionView(sess))
}

func (s *Service) sessionView(sess Session) apiSession {
	v := apiSession{
		User: apiUser{Name: sess.Name, Email: sess.Email, Initials: initials(sess.Name, sess.Email)},
		Org:  apiOrg{ID: sess.OrgID, Name: sess.OrgName, Access: sess.OrgAccess, AccessLabel: accessLabel(sess)},
		Orgs: make([]apiOrgRef, 0, len(sess.Orgs)), Projects: make([]apiProject, 0, len(sess.Projects)),
		CanCreate: sess.CanCreate(), CSRF: sess.CSRF,
		ExpiresAt: sess.CreatedAt.Add(s.sessionTTL), SessionTTL: humanDuration(s.sessionTTL),
		Uploads: s.uploadInfo(),
		Limits:  apiTextLimits{Subject: maxSubjectLen, Message: maxMessageLen, Search: maxSearchLen},
	}
	for _, o := range sess.Orgs {
		v.Orgs = append(v.Orgs, apiOrgRef{ID: string(o.ID), Name: o.Name})
	}
	for _, p := range sess.Projects {
		v.Projects = append(v.Projects, apiProject{ID: string(p.ID), Name: p.Name, Access: p.Access})
	}
	return v
}

// accessLabel describes the session's access in the current org, under its name.
func accessLabel(sess Session) string {
	switch {
	case sess.OrgAccess == AccessManage:
		return "All tickets"
	case sess.OrgAccess == AccessRead:
		return "All tickets · read-only"
	case sess.CanCreate():
		return "Your projects"
	default:
		return "Your projects · read-only"
	}
}

// APISwitchOrg makes another of the session's orgs current. Body: {"org": "<id>"}.
func (s *Service) APISwitchOrg(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, true)
	if !ok {
		return nil
	}
	var body struct {
		Org string `json:"org"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &body); err != nil || !sess.UseOrg(body.Org) {
		return s.apiFail(r, fasthttp.StatusNotFound, errNotFound, "We couldn't find that organisation.")
	}
	ctx, cancel := redisCtx()
	defer cancel()
	if err := s.store.Update(ctx, string(r.RequestCtx.Request.Header.Cookie(cookieName)), sess); err != nil {
		s.lo.Error("my-tickets: switching org", "error", err)
		return s.apiFail(r, fasthttp.StatusInternalServerError, errServer, "Something went wrong. Please try again.")
	}
	return s.apiOK(r, fasthttp.StatusOK, s.sessionView(sess))
}

// APILogout ends the session.
func (s *Service) APILogout(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, true)
	if !ok {
		return nil
	}
	ctx, cancel := redisCtx()
	defer cancel()
	if err := s.store.Delete(ctx, string(r.RequestCtx.Request.Header.Cookie(cookieName))); err != nil {
		s.lo.Error("my-tickets: signing out", "contact_id", sess.ContactID, "error", err)
	}
	r.RequestCtx.Response.Header.DelClientCookie(cookieName)
	return s.apiOK(r, fasthttp.StatusOK, map[string]bool{"signed_out": true})
}

// APIList returns a page of the inbox list. Query: status, project, q, page.
func (s *Service) APIList(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, false)
	if !ok {
		return nil
	}
	f := listFilterOf(r.RequestCtx.QueryArgs(), sess)
	rows, hasOlder, err := s.listTickets(sess, f)
	if err != nil {
		s.lo.Error("my-tickets: listing", "error", err)
		return s.apiFail(r, fasthttp.StatusInternalServerError, errServer, "We couldn't load your tickets. Please try again.")
	}
	out := apiList{
		Tickets: make([]apiTicketRow, 0, len(rows)), Status: f.Status, Project: f.Project, ProjectName: f.ProjectName,
		Search: f.Search, Page: f.Page, PerPage: defaultPerPage, HasNewer: f.Page > 1, HasOlder: hasOlder,
		ShowProject: len(sess.Projects) > 0, ShowRaisedBy: true,
	}
	for _, t := range rows {
		out.Tickets = append(out.Tickets, apiTicketRow{
			Ref: t.ReferenceNumber, Subject: t.Subject, Status: t.Status, Sender: t.Sender, FromSupport: t.Sender == supportName,
			RaisedBy: t.RaisedBy, ProjectID: t.ProjectID, ProjectName: t.ProjectName, Preview: t.Preview,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		})
	}
	return s.apiOK(r, fasthttp.StatusOK, out)
}

// APITicket returns a ticket and a page of its thread. Query: m (thread page, 1 = newest).
func (s *Service) APITicket(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, false)
	if !ok {
		return nil
	}
	t, ok := s.visibleTicket(r, &sess)
	if !ok {
		return s.apiFail(r, fasthttp.StatusNotFound, errNotFound, "We couldn't find that ticket.")
	}
	msgs, total, mpage, err := s.threadPage(t, r.RequestCtx.QueryArgs().GetUintOrZero("m"))
	if err != nil {
		s.lo.Error("my-tickets: loading messages", "error", err)
		return s.apiFail(r, fasthttp.StatusInternalServerError, errServer, "We couldn't load this ticket. Please try again.")
	}
	t = withCurrentNames(sess, t)
	newest := total - (mpage-1)*messagesPerPage // messages counted oldest = 1
	out := apiTicketView{
		Ticket: apiTicket{
			Ref: t.ReferenceNumber, Subject: t.Subject, Status: CustomerStatus(t.InternalStatus),
			ProjectID: t.ProjectID, ProjectName: t.ProjectName, RaisedBy: t.RaisedBy, RaisedByYou: t.ContactID == sess.ContactID,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		},
		Messages: make([]apiMessage, 0, len(msgs)),
		Thread: apiThread{
			Page: mpage, PerPage: messagesPerPage, Total: total,
			From: max(1, newest-messagesPerPage+1), To: max(newest, 0),
			HasEarlier: mpage*messagesPerPage < total, HasNewer: mpage > 1,
		},
		CanReply: sess.CanManageTicket(t),
		OrgID:    sess.OrgID,
	}
	for _, m := range msgs {
		author := m.AuthorName
		switch {
		case author == "" && m.FromCustomer:
			author = "Customer"
		case author == "":
			author = supportName
		}
		in := initials(m.AuthorName, "")
		if !m.FromCustomer {
			in = "ES" // the Encatch Support avatar, whoever answered
		}
		am := apiMessage{FromCustomer: m.FromCustomer, Author: author, Initials: in, HTML: s.sanitize.Sanitize(m.HTML),
			CreatedAt: m.CreatedAt, Attachments: make([]apiAttachment, 0, len(m.Attachments))}
		for _, a := range m.Attachments {
			am.Attachments = append(am.Attachments, apiAttachment{Name: a.Name, URL: a.URL})
		}
		out.Messages = append(out.Messages, am)
	}
	return s.apiOK(r, fasthttp.StatusOK, out)
}

// APINewTicket returns what the new-ticket form offers this session.
func (s *Service) APINewTicket(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, false)
	if !ok {
		return nil
	}
	if !sess.CanCreate() {
		return s.apiReadOnly(r)
	}
	out := apiNewTicket{
		Projects: []apiOrgRef{}, AllowOrgLevel: sess.CanManageOrg(),
		CanChoosePriority: PriorityEligible(s.orgTier(sess), s.eligibleTiers), Priorities: PriorityChoices,
		Uploads: s.uploadInfo(), Email: sess.Email, OrgName: sess.OrgName,
	}
	mp := sess.ManageProjects()
	for _, p := range mp {
		out.Projects = append(out.Projects, apiOrgRef{ID: string(p.ID), Name: p.Name})
	}
	// Default to the only project when org-level tickets aren't an option.
	if !sess.CanManageOrg() && len(mp) == 1 {
		out.DefaultProject = string(mp[0].ID)
	}
	return s.apiOK(r, fasthttp.StatusOK, out)
}

// APICreate raises a ticket. Multipart (or urlencoded) form: subject, message, project,
// priority and files.
func (s *Service) APICreate(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, true)
	if !ok {
		return nil
	}
	if !sess.CanCreate() {
		return s.apiReadOnly(r)
	}
	n := newTicketOf(r)
	files, problem := s.checkNewTicket(r, sess, n)
	if problem != "" {
		return s.apiFail(r, fasthttp.StatusUnprocessableEntity, errInvalid, problem)
	}
	ref, err := s.createTicket(sess, n, files)
	if err != nil {
		return s.apiFail(r, fasthttp.StatusInternalServerError, errServer, "We couldn't create your ticket. Please try again.")
	}
	return s.apiOK(r, fasthttp.StatusCreated, map[string]string{"ref": ref})
}

// APIReply adds a reply to a ticket. Multipart (or urlencoded) form: message and files.
func (s *Service) APIReply(r *fastglue.Request) error {
	sess, ok := s.apiAuth(r, true)
	if !ok {
		return nil
	}
	t, ok := s.visibleTicket(r, &sess)
	if !ok {
		return s.apiFail(r, fasthttp.StatusNotFound, errNotFound, "We couldn't find that ticket.")
	}
	if !sess.CanManageTicket(t) {
		return s.apiFail(r, fasthttp.StatusForbidden, errReadOnly, "You have read-only access to this ticket, so you can't reply.")
	}
	body := strings.TrimSpace(string(r.RequestCtx.FormValue("message")))
	files, problem := s.checkReply(r, body)
	if problem != "" {
		return s.apiFail(r, fasthttp.StatusUnprocessableEntity, errInvalid, problem)
	}
	if err := s.addReply(sess, t, body, files); err != nil {
		return s.apiFail(r, fasthttp.StatusInternalServerError, errServer, "We couldn't send your reply. Please try again.")
	}
	return s.apiOK(r, fasthttp.StatusCreated, map[string]bool{"sent": true})
}

// ---- helpers ----

const supportName = "Encatch Support"

// apiAuth loads the session, and for changes checks the CSRF header. On failure it
// writes the error response and returns false.
func (s *Service) apiAuth(r *fastglue.Request, change bool) (Session, bool) {
	sess, ok := s.session(r)
	if !ok {
		_ = s.apiFail(r, fasthttp.StatusUnauthorized, errSessionEnded,
			fmt.Sprintf("For your security, sessions last %s. Open Support again from Encatch to continue.", humanDuration(s.sessionTTL)))
		return Session{}, false
	}
	if change && !CSRFValid(sess, string(r.RequestCtx.Request.Header.Peek(csrfHeader))) {
		_ = s.apiFail(r, fasthttp.StatusForbidden, errCSRF, "Your page expired. Please reload and try again.")
		return Session{}, false
	}
	return sess, true
}

func (s *Service) apiReadOnly(r *fastglue.Request) error {
	return s.apiFail(r, fasthttp.StatusForbidden, errReadOnly,
		"You can view this organisation's support tickets, but not raise new ones. Ask your organisation admin for Manage access to Org Tickets or Project Tickets.")
}

func (s *Service) apiOK(r *fastglue.Request, code int, data any) error {
	return s.apiWrite(r, code, map[string]any{"data": data})
}

func (s *Service) apiFail(r *fastglue.Request, code int, errCode, message string) error {
	return s.apiWrite(r, code, map[string]any{"error": map[string]string{"code": errCode, "message": message}})
}

func (s *Service) apiWrite(r *fastglue.Request, code int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		s.lo.Error("my-tickets: encoding response", "error", err)
		code, b = fasthttp.StatusInternalServerError, []byte(`{"error":{"code":"server","message":"Something went wrong."}}`)
	}
	h := &r.RequestCtx.Response.Header
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	r.RequestCtx.SetStatusCode(code)
	r.RequestCtx.SetContentType("application/json; charset=utf-8")
	r.RequestCtx.SetBody(b)
	return nil
}
