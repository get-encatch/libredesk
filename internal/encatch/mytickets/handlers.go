package mytickets

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime/multipart"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/microcosm-cc/bluemonday"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"github.com/zerodha/logf"
)

const (
	cookieName = "libredesk_my_tickets"
	// The visitor's light/dark choice ("light" or "dark"; absent = follow the device).
	// Not HttpOnly: the app's toggle sets it; the server reads it to render data-theme.
	themeCookie    = "libredesk_my_tickets_theme"
	basePath       = "/my-tickets"
	maxSubjectLen  = 200
	maxMessageLen  = 20000
	defaultPerPage = 50
	// Messages per page of a ticket's thread. Long email replies quote whole threads,
	// so a page is kept small; "Show earlier messages" loads the next ten.
	messagesPerPage = 10
	maxPage         = 200 // 10,000 tickets deep; past that, search
	maxSearchLen    = 100
	maxFiles        = 5 // attachments per ticket or reply
)

// Service holds My Tickets' dependencies and serves its sign-in, the Vue app
// (spa.go) and the JSON API (api.go).
type Service struct {
	verifier      *Verifier
	store         *Store
	backend       Backend
	eligibleTiers []string
	sessionTTL    time.Duration
	lo            *logf.Logger
	sanitize      *bluemonday.Policy
	uploadLimits  func() UploadLimits
	app           fs.FS // the built Vue app; nil when it isn't built
}

// Opts configures a Service.
type Opts struct {
	Verifier      *Verifier
	Store         *Store
	Backend       Backend
	EligibleTiers []string // tiers that may choose Express/Urgent
	SessionTTL    time.Duration
	Logger        *logf.Logger
	// UploadLimits returns libredesk's current upload settings. Nil disables attachments.
	UploadLimits func() UploadLimits
	// App holds the built Vue app (index.html and assets/); nil means the embedded build.
	App fs.FS
}

func New(o Opts) (*Service, error) {
	app := o.App
	if app == nil {
		app = builtApp
	}
	if app == nil {
		o.Logger.Error("my-tickets: the Vue app isn't built into the binary; /my-tickets will answer 503 (build it with pnpm in internal/encatch/mytickets/web)")
	}
	return &Service{
		verifier: o.Verifier, store: o.Store, backend: o.Backend, eligibleTiers: o.EligibleTiers,
		sessionTTL: o.SessionTTL, lo: o.Logger, sanitize: bluemonday.UGCPolicy(),
		uploadLimits: o.UploadLimits, app: app,
	}, nil
}

// ---- sign-in ----

// Login exchanges a token (query or form field "token") for a session.
func (s *Service) Login(r *fastglue.Request) error {
	token := string(r.RequestCtx.QueryArgs().Peek("token"))
	if token == "" {
		token = string(r.RequestCtx.FormValue("token"))
	}
	if token == "" {
		return s.signInFailed(r, signInMissing)
	}
	claims, err := s.verifier.Verify(token)
	if err != nil {
		s.lo.Warn("my-tickets: token rejected", "error", err, "ip", r.RequestCtx.RemoteIP().String())
		return s.signInFailed(r, signInInvalid)
	}
	ctx, cancel := redisCtx()
	defer cancel()
	fresh, err := s.store.UseJTI(ctx, claims.Issuer, claims.ID, claims.ExpiresAt.Add(s.verifier.Leeway))
	if err != nil {
		s.lo.Error("my-tickets: recording jti", "error", err)
		return s.signInFailed(r, signInError)
	}
	if !fresh {
		s.lo.Warn("my-tickets: token replayed", "iss", claims.Issuer, "jti", claims.ID)
		return s.signInFailed(r, signInUsed)
	}

	first, last := splitName(claims.Name, claims.Email)
	contactID, err := s.backend.ResolveContact(string(claims.ExternalUserID), claims.Email, first, last)
	if err != nil {
		s.lo.Error("my-tickets: resolving contact", "error", err)
		return s.signInFailed(r, signInError)
	}
	sess := Session{
		ContactID: contactID, Email: claims.Email, Name: strings.TrimSpace(first + " " + last),
		Issuer: claims.Issuer, Instance: claims.Instance, Orgs: claims.Orgs,
	}
	// Record every org (the helpdesk owns their support tiers) and open the org of the
	// customer's most recent ticket, else the first by name.
	orgIDs := make([]string, 0, len(sess.Orgs))
	for _, o := range sess.Orgs {
		orgIDs = append(orgIDs, string(o.ID))
		if _, err := s.backend.RegisterOrg(sess.Instance, string(o.ID), o.Name); err != nil {
			s.lo.Error("my-tickets: registering org", "org", o.ID, "error", err)
		}
		if err := s.backend.SyncNames(o); err != nil {
			s.lo.Error("my-tickets: updating renamed org or project names on tickets", "org", o.ID, "error", err)
		}
	}
	current, err := s.backend.LatestTicketOrg(contactID, orgIDs)
	if err != nil {
		s.lo.Error("my-tickets: finding the latest ticket's org", "error", err)
	}
	if current == "" || !sess.UseOrg(current) {
		sort.SliceStable(sess.Orgs, func(i, j int) bool { return strings.ToLower(sess.Orgs[i].Name) < strings.ToLower(sess.Orgs[j].Name) })
		sess.UseOrg(string(sess.Orgs[0].ID))
	}
	sid, err := s.store.Create(ctx, sess)
	if err != nil {
		s.lo.Error("my-tickets: creating session", "error", err)
		return s.signInFailed(r, signInError)
	}
	s.lo.Info("my-tickets: sign-in", "iss", claims.Issuer, "instance", sess.Instance, "user", string(claims.ExternalUserID), "orgs", len(sess.Orgs), "org", sess.OrgID, "scope", sess.Scope, "contact_id", contactID)

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

// Why a sign-in failed, for the app to explain (?signin=<reason> on /my-tickets). The
// codes are fixed: the app maps each to its message and ignores anything else.
const (
	signInMissing = "missing" // no token in the link
	signInInvalid = "invalid" // bad signature, expired, unknown issuer, no access
	signInUsed    = "used"    // the token was already exchanged
	signInError   = "error"   // the helpdesk couldn't record the sign-in
)

// signInFailed sends the browser to the app, which explains the failure, or simply
// opens the customer's tickets if they still have a session from an earlier sign-in.
func (s *Service) signInFailed(r *fastglue.Request, reason string) error {
	return s.redirect(r, basePath+"?signin="+reason)
}

// ---- shared by the API handlers ----

// inboxRow is one ticket in the inbox list.
type inboxRow struct {
	TicketSummary
	Status string
	Sender string
}

// listFilter is the inbox list's filters and page.
type listFilter struct {
	Status, Project, ProjectName, Search string
	Page                                 int
}

// listFilterOf reads the list filters from a query string, dropping anything invalid
// (an unknown status, a project the session can't see).
func listFilterOf(args *fasthttp.Args, sess Session) listFilter {
	f := listFilter{Status: string(args.Peek("status")), Project: string(args.Peek("project"))}
	if f.Status != StatusOpen && f.Status != StatusWaitingYou && f.Status != StatusResolved {
		f.Status = ""
	}
	var ok bool
	if f.ProjectName, ok = sess.ProjectName(f.Project); !ok {
		f.Project = ""
	}
	f.Search = strings.TrimSpace(string(args.Peek("q")))
	if r := []rune(f.Search); len(r) > maxSearchLen {
		f.Search = string(r[:maxSearchLen])
	}
	f.Page = min(max(args.GetUintOrZero("page"), 1), maxPage)
	return f
}

// listTickets loads one page of the inbox list, and whether there is an older page.
func (s *Service) listTickets(sess Session, f listFilter) ([]inboxRow, bool, error) {
	// One extra row says whether there is an older page, without counting the org's tickets.
	tickets, err := s.backend.ListTickets(ListQuery{
		Scope: sess.Scope, ContactID: sess.ContactID, OrgID: sess.OrgID, ProjectIDs: sess.ProjectIDs(),
		Status: f.Status, ProjectID: f.Project, Search: f.Search,
		Limit: defaultPerPage + 1, Offset: (f.Page - 1) * defaultPerPage,
	})
	if err != nil {
		return nil, false, err
	}
	hasOlder := len(tickets) > defaultPerPage && f.Page < maxPage
	if len(tickets) > defaultPerPage {
		tickets = tickets[:defaultPerPage]
	}
	rows := make([]inboxRow, 0, len(tickets))
	for _, t := range tickets {
		t = withCurrentNames(sess, t)
		rows = append(rows, inboxRow{t, CustomerStatus(t.InternalStatus), sender(sess, t)})
	}
	return rows, hasOlder, nil
}

// threadPage loads page mpage of a ticket's thread (1, the default, is the newest
// messages; a page past the oldest shows the oldest). It returns the messages, how many
// there are in all, and the page shown.
func (s *Service) threadPage(t TicketSummary, mpage int) ([]Message, int, int, error) {
	mpage = max(mpage, 1)
	msgs, total, err := s.backend.Messages(t.UUID, mpage, messagesPerPage)
	if err == nil && len(msgs) == 0 && total > 0 && mpage > 1 {
		mpage = (total + messagesPerPage - 1) / messagesPerPage
		msgs, total, err = s.backend.Messages(t.UUID, mpage, messagesPerPage)
	}
	return msgs, total, mpage, err
}

// newTicket is the new-ticket form's fields.
type newTicket struct{ Subject, Body, Project, Priority string }

func newTicketOf(r *fastglue.Request) newTicket {
	v := func(k string) string { return strings.TrimSpace(string(r.RequestCtx.FormValue(k))) }
	return newTicket{Subject: v("subject"), Body: v("message"), Project: v("project"), Priority: v("priority")}
}

// checkNewTicket checks a new ticket from a session that may create tickets, and
// reads its attachments. It returns the files, or a problem to show the customer.
func (s *Service) checkNewTicket(r *fastglue.Request, sess Session, n newTicket) ([]Upload, string) {
	var problem string
	switch {
	case n.Subject == "":
		problem = "Please add a subject."
	case len(n.Subject) > maxSubjectLen:
		problem = fmt.Sprintf("Please keep the subject under %d characters.", maxSubjectLen)
	case n.Body == "":
		problem = "Please describe the problem or question."
	case len(n.Body) > maxMessageLen:
		problem = fmt.Sprintf("Please keep the message under %d characters.", maxMessageLen)
	}
	_, projectOK := sess.ProjectName(n.Project)
	switch {
	case n.Project == "" && !sess.CanManageOrg():
		problem = "Please choose one of your projects."
	case n.Project != "" && (!projectOK || !sess.CanManageProject(n.Project)):
		problem = "Please choose one of your projects."
	}
	files, fileProblem := s.readUploads(r)
	if problem == "" {
		problem = fileProblem
	}
	if problem != "" {
		return nil, problem
	}
	return files, ""
}

// createTicket creates a checked new ticket and returns its reference number.
func (s *Service) createTicket(sess Session, n newTicket, files []Upload) (string, error) {
	attrs := map[string]any{
		AttrOrgID: sess.OrgID, AttrOrgName: sess.OrgName, AttrSourceApp: sess.Issuer, AttrInstance: sess.Instance,
	}
	tier := s.orgTier(sess) // current tier: an admin may have changed it since sign-in
	if tier != "" {
		attrs[AttrTicketTier] = tier
	}
	if n.Project != "" {
		projectName, _ := sess.ProjectName(n.Project)
		attrs[AttrProjectID] = n.Project
		attrs[AttrProjectName] = projectName
	}
	if PriorityEligible(tier, s.eligibleTiers) && contains(PriorityChoices, n.Priority) {
		attrs[AttrRequestedPriority] = n.Priority
	}
	ref, err := s.backend.CreateTicket(sess.ContactID, n.Subject, textToHTML(n.Body), attrs, []string{InstanceTag(sess.Instance)}, files)
	if err != nil {
		s.lo.Error("my-tickets: creating ticket", "error", err)
		return "", err
	}
	s.lo.Info("my-tickets: ticket created", "ref", ref, "iss", sess.Issuer, "org", sess.OrgID, "contact_id", sess.ContactID, "files", len(files))
	return ref, nil
}

// checkReply checks a reply and reads its attachments. It returns the files, or a
// problem to show the customer.
func (s *Service) checkReply(r *fastglue.Request, body string) ([]Upload, string) {
	files, problem := s.readUploads(r)
	switch {
	case problem != "":
	case body == "" && len(files) == 0:
		problem = "Please write a reply or attach a file."
	case len(body) > maxMessageLen:
		problem = fmt.Sprintf("Please keep the reply under %d characters.", maxMessageLen)
	}
	if problem != "" {
		return nil, problem
	}
	return files, ""
}

// addReply adds a checked reply to a ticket the session may reply to.
func (s *Service) addReply(sess Session, t TicketSummary, body string, files []Upload) error {
	html := ""
	if body != "" {
		html = textToHTML(body)
	}
	if err := s.backend.AddReply(sess.ContactID, t.UUID, html, files); err != nil {
		s.lo.Error("my-tickets: adding reply", "error", err)
		return err
	}
	return nil
}

// ---- helpers ----

// themeOf returns the visitor's saved theme: "light", "dark", or "" (follow the device).
func themeOf(r *fastglue.Request) string {
	switch t := string(r.RequestCtx.Request.Header.Cookie(themeCookie)); t {
	case "light", "dark":
		return t
	}
	return ""
}

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

// withCurrentNames shows the project's current name from the sign-in, rather than the
// name stored on the ticket when it was raised (SyncNames catches the stored one up).
func withCurrentNames(sess Session, t TicketSummary) TicketSummary {
	if name, ok := sess.ProjectName(t.ProjectID); ok && t.ProjectID != "" {
		t.ProjectName = name
	}
	return t
}

// visibleTicket loads the ticket in the URL if the session may see it. A ticket in
// another of the session's orgs (a link opened while a different org was selected)
// switches the session to that org, if the user's access there shows it. Anything
// else is "not found", whether or not the ticket exists.
func (s *Service) visibleTicket(r *fastglue.Request, sess *Session) (TicketSummary, bool) {
	ref, _ := r.RequestCtx.UserValue("ref").(string)
	if ref == "" || len(ref) > 64 {
		return TicketSummary{}, false
	}
	t, err := s.backend.GetTicket(ref)
	if err != nil {
		return TicketSummary{}, false
	}
	if Visible(*sess, t) {
		return t, true
	}
	switched := *sess
	if t.OrgID == "" || !switched.UseOrg(t.OrgID) || !Visible(switched, t) {
		return TicketSummary{}, false
	}
	ctx, cancel := redisCtx()
	defer cancel()
	if err := s.store.Update(ctx, string(r.RequestCtx.Request.Header.Cookie(cookieName)), switched); err != nil {
		s.lo.Error("my-tickets: switching org for a ticket link", "error", err)
		return TicketSummary{}, false
	}
	*sess = switched
	return t, true
}

// orgTier returns the org's current support tier, falling back to the tier at sign-in.
func (s *Service) orgTier(sess Session) string {
	tier, err := s.backend.OrgTier(sess.OrgID)
	if err != nil {
		s.lo.Error("my-tickets: reading org tier", "org", sess.OrgID, "error", err)
		return sess.SupportTier
	}
	return tier
}

// uploadView describes the attachment limits for the forms; nil hides the file field.
type uploadView struct {
	MaxMB      int      `json:"max_mb"`
	MaxTotalMB int      `json:"max_total_mb"` // 0 when it wouldn't limit anything beyond MaxFiles × MaxMB
	MaxFiles   int      `json:"max_files"`
	Accept     string   `json:"accept"`     // the file input's accept attribute, e.g. ".pdf,.png"; "" allows any
	Extensions []string `json:"extensions"` // lowercase, without dots; ["*"] allows any
}

func (s *Service) uploadInfo() *uploadView {
	if s.uploadLimits == nil {
		return nil
	}
	l := s.uploadLimits()
	if len(l.Extensions) == 0 || l.MaxMB <= 0 {
		return nil // nothing can be attached
	}
	v := &uploadView{MaxMB: l.MaxMB, MaxFiles: maxFiles, Extensions: l.Extensions}
	if l.MaxTotalMB > 0 && l.MaxTotalMB < maxFiles*l.MaxMB {
		v.MaxTotalMB = l.MaxTotalMB
	}
	if !contains(l.Extensions, "*") {
		exts := make([]string, 0, len(l.Extensions))
		for _, e := range l.Extensions {
			exts = append(exts, "."+e)
		}
		v.Accept = strings.Join(exts, ",")
	}
	return v
}

// readUploads returns the files attached to a multipart form, checked against
// libredesk's upload settings, or a problem to show the customer.
func (s *Service) readUploads(r *fastglue.Request) ([]Upload, string) {
	form, err := r.RequestCtx.MultipartForm()
	if err != nil {
		return nil, "" // not a multipart form: no files
	}
	var headers []*multipart.FileHeader
	for _, fh := range form.File["files"] {
		// Browsers send an empty part when no file was chosen.
		if fh.Filename == "" && fh.Size == 0 {
			continue
		}
		headers = append(headers, fh)
	}
	if len(headers) == 0 {
		return nil, ""
	}
	if s.uploadLimits == nil {
		return nil, "Attachments aren't available right now. Please send your message without them."
	}
	if len(headers) > maxFiles {
		return nil, fmt.Sprintf("Please attach up to %d files.", maxFiles)
	}
	limits := s.uploadLimits()
	maxBytes := int64(limits.MaxMB) << 20
	if limits.MaxTotalMB > 0 {
		var total int64
		for _, fh := range headers {
			total += fh.Size
		}
		if total > int64(limits.MaxTotalMB)<<20 {
			return nil, fmt.Sprintf("Attachments can be up to %d MB in total. Please send fewer or smaller files.", limits.MaxTotalMB)
		}
	}
	files := make([]Upload, 0, len(headers))
	for _, fh := range headers {
		name := filepath.Base(fh.Filename)
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		switch {
		case fh.Size <= 0:
			return nil, fmt.Sprintf("%q is empty.", name)
		case fh.Size > maxBytes:
			return nil, fmt.Sprintf("%q is larger than %d MB.", name, limits.MaxMB)
		case !contains(limits.Extensions, "*") && !contains(limits.Extensions, ext):
			return nil, fmt.Sprintf("%q can't be attached: that file type isn't allowed.", name)
		}
		f, err := fh.Open()
		if err != nil {
			s.lo.Error("my-tickets: opening upload", "error", err)
			return nil, "We couldn't read your attachment. Please try again."
		}
		data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
		f.Close()
		if err != nil || int64(len(data)) > maxBytes {
			return nil, "We couldn't read your attachment. Please try again."
		}
		files = append(files, Upload{Name: name, ContentType: fh.Header.Get("Content-Type"), Data: data})
	}
	return files, ""
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
// sender names who wrote a ticket's latest message, as the list shows it.
func sender(sess Session, t TicketSummary) string {
	switch {
	case t.LastAuthorID == 0 && t.Preview == "":
		return t.RaisedBy
	case !t.LastFromCustomer:
		return supportName
	case t.LastAuthorID == sess.ContactID:
		return "You"
	case t.LastAuthor != "":
		return t.LastAuthor
	default:
		return t.RaisedBy
	}
}

// humanDuration formats a session length for customers ("2 hours", "30 minutes").
func humanDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		if h := int(d / time.Hour); h == 1 {
			return "1 hour"
		} else {
			return fmt.Sprintf("%d hours", h)
		}
	}
	return fmt.Sprintf("%d minutes", int(d.Round(time.Minute)/time.Minute))
}

// initials returns up to two letters for the sidebar avatar.
func initials(name, email string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		words = []string{email}
	}
	var out []rune
	for _, w := range words {
		for _, r := range w {
			out = append(out, unicode.ToUpper(r))
			break
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

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
