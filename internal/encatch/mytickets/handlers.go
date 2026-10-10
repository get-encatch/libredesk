package mytickets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"github.com/zerodha/logf"
)

//go:embed templates/*.html
var templateFS embed.FS

// app.css is built from ui/ (Tailwind + libredesk's shadcn tokens); run ui/build.sh
// after changing templates.
//
//go:embed static/app.css
var appCSS []byte

// cssVersion busts browser caches when the stylesheet changes.
var cssVersion = func() string {
	sum := sha256.Sum256(appCSS)
	return hex.EncodeToString(sum[:])[:12]
}()

const (
	cookieName     = "libredesk_my_tickets"
	basePath       = "/my-tickets"
	maxSubjectLen  = 200
	maxMessageLen  = 20000
	defaultPerPage = 200
)

// Service holds My Tickets' dependencies and serves its pages.
type Service struct {
	verifier      *Verifier
	store         *Store
	backend       Backend
	eligibleTiers []string
	sessionTTL    time.Duration
	lo            *logf.Logger
	tmpl          *template.Template
	sanitize      *bluemonday.Policy
}

// Opts configures a Service.
type Opts struct {
	Verifier      *Verifier
	Store         *Store
	Backend       Backend
	EligibleTiers []string // tiers that may choose Express/Urgent
	SessionTTL    time.Duration
	Logger        *logf.Logger
}

func New(o Opts) (*Service, error) {
	funcs := template.FuncMap{
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				if k, ok := kv[i].(string); ok {
					m[k] = kv[i+1]
				}
			}
			return m
		},
		"date":       func(t time.Time) string { return t.In(ist).Format("2 Jan 2006, 15:04") },
		"cssVersion": func() string { return cssVersion },
	}
	tmpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing my-tickets templates: %w", err)
	}
	return &Service{
		verifier: o.Verifier, store: o.Store, backend: o.Backend, eligibleTiers: o.EligibleTiers,
		sessionTTL: o.SessionTTL, lo: o.Logger, tmpl: tmpl, sanitize: bluemonday.UGCPolicy(),
	}, nil
}

var ist = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Kolkata"); err == nil {
		return l
	}
	return time.FixedZone("IST", 5*3600+1800)
}()

// ---- pages ----

// Login exchanges a token (query or form field "token") for a session.
func (s *Service) Login(r *fastglue.Request) error {
	token := string(r.RequestCtx.QueryArgs().Peek("token"))
	if token == "" {
		token = string(r.RequestCtx.FormValue("token"))
	}
	if token == "" {
		return s.renderError(r, fasthttp.StatusBadRequest, "This link is missing its sign-in token. Open Support again from your Encatch app.")
	}
	claims, err := s.verifier.Verify(token)
	if err != nil {
		s.lo.Warn("my-tickets: token rejected", "error", err, "ip", r.RequestCtx.RemoteIP().String())
		return s.renderError(r, fasthttp.StatusUnauthorized, "This sign-in link is invalid or has expired. Open Support again from your Encatch app.")
	}
	ctx, cancel := redisCtx()
	defer cancel()
	fresh, err := s.store.UseJTI(ctx, claims.Issuer, claims.ID, claims.ExpiresAt.Add(s.verifier.Leeway))
	if err != nil {
		s.lo.Error("my-tickets: recording jti", "error", err)
		return s.renderError(r, fasthttp.StatusInternalServerError, "Something went wrong. Please try again.")
	}
	if !fresh {
		s.lo.Warn("my-tickets: token replayed", "iss", claims.Issuer, "jti", claims.ID)
		return s.renderError(r, fasthttp.StatusUnauthorized, "This sign-in link has already been used. Open Support again from your Encatch app.")
	}

	first, last := splitName(claims.Name, claims.Email)
	contactID, err := s.backend.ResolveContact(string(claims.ExternalUserID), claims.Email, first, last)
	if err != nil {
		s.lo.Error("my-tickets: resolving contact", "error", err)
		return s.renderError(r, fasthttp.StatusInternalServerError, "Something went wrong. Please try again.")
	}
	sess := Session{
		ContactID: contactID, Email: claims.Email, Name: strings.TrimSpace(first + " " + last),
		Issuer: claims.Issuer, OrgID: string(claims.OrgID), OrgName: strings.TrimSpace(claims.OrgName),
		SupportTier: claims.SupportTier, Scope: claims.Scope, Projects: claims.Projects,
		CurrentProjectID: string(claims.CurrentProjectID),
	}
	sid, err := s.store.Create(ctx, sess)
	if err != nil {
		s.lo.Error("my-tickets: creating session", "error", err)
		return s.renderError(r, fasthttp.StatusInternalServerError, "Something went wrong. Please try again.")
	}
	s.lo.Info("my-tickets: sign-in", "iss", claims.Issuer, "user", string(claims.ExternalUserID), "org", sess.OrgID, "scope", sess.Scope, "contact_id", contactID)

	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey(cookieName)
	c.SetValue(sid)
	c.SetPath(basePath)
	c.SetHTTPOnly(true)
	c.SetSecure(true)
	c.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	c.SetMaxAge(int(s.sessionTTL.Seconds()))
	r.RequestCtx.Response.Header.SetCookie(c)
	// Redirect to a clean URL so the token leaves the address bar and history.
	return s.redirect(r, basePath)
}

// List shows the tickets in the session's scope.
func (s *Service) List(r *fastglue.Request) error {
	sess, ok := s.session(r)
	if !ok {
		return s.sessionEnded(r)
	}
	status := string(r.RequestCtx.QueryArgs().Peek("status"))
	project := string(r.RequestCtx.QueryArgs().Peek("project"))
	if status != StatusOpen && status != StatusWaitingYou && status != StatusResolved {
		status = ""
	}
	if _, ok := sess.ProjectName(project); !ok {
		project = ""
	}
	tickets, err := s.backend.ListTickets(ListQuery{
		Scope: sess.Scope, ContactID: sess.ContactID, OrgID: sess.OrgID, ProjectIDs: sess.ProjectIDs(),
		Status: status, ProjectID: project, Limit: defaultPerPage,
	})
	if err != nil {
		s.lo.Error("my-tickets: listing", "error", err)
		return s.renderError(r, fasthttp.StatusInternalServerError, "We couldn't load your tickets. Please try again.")
	}
	type row struct {
		TicketSummary
		Status string
	}
	rows := make([]row, 0, len(tickets))
	for _, t := range tickets {
		rows = append(rows, row{t, CustomerStatus(t.InternalStatus)})
	}
	return s.render(r, fasthttp.StatusOK, "list.html", map[string]any{
		"S": sess, "Tickets": rows, "Status": status, "Project": project,
		"Statuses":     []string{StatusOpen, StatusWaitingYou, StatusResolved},
		"ShowRaisedBy": sess.Scope != ScopeSelf, "ShowProject": len(sess.Projects) > 0,
	})
}

// View shows one ticket's thread.
func (s *Service) View(r *fastglue.Request) error {
	sess, ok := s.session(r)
	if !ok {
		return s.sessionEnded(r)
	}
	t, ok := s.visibleTicket(r, sess)
	if !ok {
		return s.renderError(r, fasthttp.StatusNotFound, "We couldn't find that ticket.")
	}
	msgs, err := s.backend.Messages(t.UUID)
	if err != nil {
		s.lo.Error("my-tickets: loading messages", "error", err)
		return s.renderError(r, fasthttp.StatusInternalServerError, "We couldn't load this ticket. Please try again.")
	}
	type viewMsg struct {
		Message
		Body template.HTML
	}
	view := make([]viewMsg, 0, len(msgs))
	for _, m := range msgs {
		view = append(view, viewMsg{m, template.HTML(s.sanitize.Sanitize(m.HTML))}) // #nosec G203: sanitised above
	}
	return s.render(r, fasthttp.StatusOK, "ticket.html", map[string]any{
		"S": sess, "T": t, "Status": CustomerStatus(t.InternalStatus), "Messages": view,
		"Sent": string(r.RequestCtx.QueryArgs().Peek("sent")) == "1",
	})
}

// NewForm shows the new-ticket form.
func (s *Service) NewForm(r *fastglue.Request) error {
	sess, ok := s.session(r)
	if !ok {
		return s.sessionEnded(r)
	}
	return s.renderNewForm(r, sess, "", "", sess.CurrentProjectID, "Normal", "")
}

// Create handles the new-ticket form.
func (s *Service) Create(r *fastglue.Request) error {
	sess, ok := s.session(r)
	if !ok {
		return s.sessionEnded(r)
	}
	if !CSRFValid(sess, string(r.RequestCtx.FormValue("csrf"))) {
		return s.renderError(r, fasthttp.StatusForbidden, "Your form expired. Please go back and try again.")
	}
	subject := strings.TrimSpace(string(r.RequestCtx.FormValue("subject")))
	body := strings.TrimSpace(string(r.RequestCtx.FormValue("message")))
	project := strings.TrimSpace(string(r.RequestCtx.FormValue("project")))
	priority := strings.TrimSpace(string(r.RequestCtx.FormValue("priority")))

	var problem string
	switch {
	case subject == "":
		problem = "Please add a subject."
	case len(subject) > maxSubjectLen:
		problem = fmt.Sprintf("Please keep the subject under %d characters.", maxSubjectLen)
	case body == "":
		problem = "Please describe the problem or question."
	case len(body) > maxMessageLen:
		problem = fmt.Sprintf("Please keep the message under %d characters.", maxMessageLen)
	}
	projectName, projectOK := sess.ProjectName(project)
	if project != "" && !projectOK {
		problem = "Please choose one of your projects."
	}
	if problem != "" {
		return s.renderNewForm(r, sess, subject, body, project, priority, problem)
	}

	attrs := map[string]any{
		AttrOrgID: sess.OrgID, AttrOrgName: sess.OrgName, AttrSourceApp: sess.Issuer,
	}
	if sess.SupportTier != "" {
		attrs[AttrTicketTier] = sess.SupportTier
	}
	if project != "" {
		attrs[AttrProjectID] = project
		attrs[AttrProjectName] = projectName
	}
	if PriorityEligible(sess.SupportTier, s.eligibleTiers) && contains(PriorityChoices, priority) {
		attrs[AttrRequestedPriority] = priority
	}
	ref, err := s.backend.CreateTicket(sess.ContactID, subject, textToHTML(body), attrs)
	if err != nil {
		s.lo.Error("my-tickets: creating ticket", "error", err)
		return s.renderNewForm(r, sess, subject, body, project, priority, "We couldn't create your ticket. Please try again.")
	}
	s.lo.Info("my-tickets: ticket created", "ref", ref, "iss", sess.Issuer, "org", sess.OrgID, "contact_id", sess.ContactID)
	return s.redirect(r, basePath+"/"+ref)
}

// Reply adds a customer reply to a ticket.
func (s *Service) Reply(r *fastglue.Request) error {
	sess, ok := s.session(r)
	if !ok {
		return s.sessionEnded(r)
	}
	if !CSRFValid(sess, string(r.RequestCtx.FormValue("csrf"))) {
		return s.renderError(r, fasthttp.StatusForbidden, "Your form expired. Please go back and try again.")
	}
	t, ok := s.visibleTicket(r, sess)
	if !ok {
		return s.renderError(r, fasthttp.StatusNotFound, "We couldn't find that ticket.")
	}
	body := strings.TrimSpace(string(r.RequestCtx.FormValue("message")))
	if body == "" || len(body) > maxMessageLen {
		return s.redirect(r, basePath+"/"+t.ReferenceNumber)
	}
	if err := s.backend.AddReply(sess.ContactID, t.UUID, textToHTML(body)); err != nil {
		s.lo.Error("my-tickets: adding reply", "error", err)
		return s.renderError(r, fasthttp.StatusInternalServerError, "We couldn't send your reply. Please try again.")
	}
	return s.redirect(r, basePath+"/"+t.ReferenceNumber+"?sent=1")
}

// CSS serves the stylesheet. URLs carry ?v=<hash>, so it can be cached long-term.
func (s *Service) CSS(r *fastglue.Request) error {
	r.RequestCtx.Response.Header.Set("Cache-Control", "public, max-age=31536000, immutable")
	r.RequestCtx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	r.RequestCtx.SetContentType("text/css; charset=utf-8")
	r.RequestCtx.SetBody(appCSS)
	return nil
}

// Logout ends the session.
func (s *Service) Logout(r *fastglue.Request) error {
	sid := string(r.RequestCtx.Request.Header.Cookie(cookieName))
	ctx, cancel := redisCtx()
	defer cancel()
	if sess, err := s.store.Get(ctx, sid); err == nil && CSRFValid(sess, string(r.RequestCtx.FormValue("csrf"))) {
		_ = s.store.Delete(ctx, sid)
	}
	r.RequestCtx.Response.Header.DelClientCookie(cookieName)
	return s.render(r, fasthttp.StatusOK, "message.html", map[string]any{
		"Title": "Signed out", "Text": "You've signed out of Encatch Support. Open Support from your Encatch app to sign in again.",
	})
}

// ---- helpers ----

// redisCtx bounds each Redis call; the request object isn't used as a context.
func redisCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (s *Service) session(r *fastglue.Request) (Session, bool) {
	ctx, cancel := redisCtx()
	defer cancel()
	sess, err := s.store.Get(ctx, string(r.RequestCtx.Request.Header.Cookie(cookieName)))
	if err != nil {
		if !errors.Is(err, ErrNoSession) {
			s.lo.Error("my-tickets: loading session", "error", err)
		}
		return Session{}, false
	}
	return sess, true
}

func (s *Service) visibleTicket(r *fastglue.Request, sess Session) (TicketSummary, bool) {
	ref, _ := r.RequestCtx.UserValue("ref").(string)
	if ref == "" || len(ref) > 64 {
		return TicketSummary{}, false
	}
	t, err := s.backend.GetTicket(ref)
	if err != nil || !Visible(sess, t) {
		return TicketSummary{}, false
	}
	return t, true
}

func (s *Service) renderNewForm(r *fastglue.Request, sess Session, subject, body, project, priority, problem string) error {
	return s.render(r, fasthttp.StatusOK, "new.html", map[string]any{
		"S": sess, "Subject": subject, "Message": body, "Project": project, "Priority": priority, "Problem": problem,
		"CanChoosePriority": PriorityEligible(sess.SupportTier, s.eligibleTiers), "Priorities": PriorityChoices,
	})
}

func (s *Service) sessionEnded(r *fastglue.Request) error {
	return s.render(r, fasthttp.StatusUnauthorized, "message.html", map[string]any{
		"Title": "Your session has ended", "Text": "For your security, sessions last 8 hours. Open Support again from your Encatch app to continue.",
	})
}

func (s *Service) renderError(r *fastglue.Request, code int, text string) error {
	return s.render(r, code, "message.html", map[string]any{"Title": "Something's not right", "Text": text})
}

func (s *Service) render(r *fastglue.Request, code int, name string, data map[string]any) error {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.lo.Error("my-tickets: rendering", "template", name, "error", err)
		code = fasthttp.StatusInternalServerError
		buf.Reset()
		buf.WriteString("Something went wrong.")
	}
	setSecurityHeaders(r.RequestCtx)
	r.RequestCtx.SetStatusCode(code)
	r.RequestCtx.SetContentType("text/html; charset=utf-8")
	r.RequestCtx.SetBody(buf.Bytes())
	return nil
}

func (s *Service) redirect(r *fastglue.Request, to string) error {
	setSecurityHeaders(r.RequestCtx)
	// A relative Location: fasthttp's Redirect would build an absolute http:// URL
	// (TLS ends at Caddy), forcing an extra HTTPS hop.
	r.RequestCtx.Response.Header.Set("Location", to)
	r.RequestCtx.SetStatusCode(fasthttp.StatusSeeOther)
	return nil
}

func setSecurityHeaders(ctx *fasthttp.RequestCtx) {
	h := &ctx.Response.Header
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
}

// textToHTML turns plain text from a form into safe HTML paragraphs.
func textToHTML(text string) string {
	var b strings.Builder
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(html.EscapeString(para), "\n", "<br>"))
		b.WriteString("</p>")
	}
	return b.String()
}

// splitName splits a display name, falling back to the email's local part.
func splitName(name, email string) (string, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		if i := strings.Index(email, "@"); i > 0 {
			return email[:i], ""
		}
		return email, ""
	}
	parts := strings.Fields(name)
	return parts[0], strings.Join(parts[1:], " ")
}
