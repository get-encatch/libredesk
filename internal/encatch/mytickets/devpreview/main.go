// Command devpreview runs the My Tickets pages locally with sample data, for UI
// review. It needs no database or libredesk server: an in-memory Redis and a fake
// backend stand in. Not part of the libredesk binary.
//
//	go run ./internal/encatch/mytickets/devpreview      # then open http://localhost:8790
package main

import (
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abhinavxd/libredesk/internal/encatch/mytickets"
	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/redis/go-redis/v9"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"github.com/zerodha/logf"
)

const (
	addr   = "localhost:8790"
	secret = "preview-secret"
)

var tiers = []string{"SaaS Standard", "SaaS Growth", "Growth Plus", "Enterprise Standard", "Enterprise Premium"}

func main() {
	mr, err := miniredis.Run()
	if err != nil {
		log.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	lo := logf.New(logf.Opts{Level: logf.InfoLevel})
	backend := newSampleBackend()
	svc, err := mytickets.New(mytickets.Opts{
		Verifier: &mytickets.Verifier{Secrets: map[string][]string{"encatch_accounts_local": {secret}},
			MaxLifetime: time.Minute, Leeway: 30 * time.Second},
		Store:         mytickets.NewStore(rdb, 8*time.Hour),
		Backend:       backend,
		EligibleTiers: []string{"Growth Plus", "Enterprise Standard", "Enterprise Premium"},
		SessionTTL:    8 * time.Hour,
		Logger:        &lo,
		UploadLimits: func() mytickets.UploadLimits {
			// Same customer allowlist as deploy/config.toml (my_tickets.allowed_extensions).
			return mytickets.UploadLimits{MaxMB: 20, MaxTotalMB: 50, Extensions: []string{"png", "jpg", "jpeg", "gif", "webp", "heic", "pdf", "txt", "log",
				"csv", "json", "xml", "md", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "zip", "mp4", "mov", "webm", "mp3", "m4a", "wav", "har"}}
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	g := fastglue.NewGlue()
	g.GET("/", index)
	g.GET("/preview-login", func(r *fastglue.Request) error {
		// The sign-in links pick a tier: act as an admin who set it for the org.
		if tier := string(r.RequestCtx.QueryArgs().Peek("tier")); tier != "" {
			backend.setTier("local-42", tier) // BigCorp
		}
		return previewLogin(r)
	})
	g.GET("/my-tickets/assets/{file}", svc.Asset)
	g.GET("/my-tickets/login", svc.Login)
	g.POST("/my-tickets/logout", svc.Logout)
	g.GET("/my-tickets", svc.List)
	g.GET("/my-tickets/new", svc.NewForm)
	g.POST("/my-tickets/new", svc.Create)
	g.POST("/my-tickets/org", svc.SwitchOrg)
	g.GET("/my-tickets/{ref}", svc.View)
	g.POST("/my-tickets/{ref}/reply", svc.Reply)
	g.GET("/preview-uploads/{id}", backend.serveUpload)
	// The Geist font, as libredesk serves it.
	fonts := fasthttp.FSHandler(repoRoot()+"/static/public/static", 3)
	g.GET("/static/public/static/{path:*}", func(r *fastglue.Request) error { fonts(r.RequestCtx); return nil })

	log.Printf("My Tickets preview on http://%s", addr)
	srv := &fasthttp.Server{Handler: g.Handler()}
	log.Fatal(srv.ListenAndServe(addr))
}

// index lists one-click sign-ins, one per kind of access.
func index(r *fastglue.Request) error {
	type opt struct{ label, query string }
	opts := []opt{
		{"Anita: manages BigCorp (org_tickets:2), reads Acme's iOS app (project_tickets:4)", "user=anita&tier=Growth+Plus"},
		{"Ravi: reads all of BigCorp (org_tickets:4), read-only", "user=ravi&tier=Enterprise+Premium"},
		{"Meera: manages BigCorp's Mobile app, reads Website (project_tickets)", "user=meera&tier=Growth+Plus"},
		{"Sam: manages Acme only, no tickets yet (SaaS Standard)", "user=sam&tier=SaaS+Standard"},
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset=utf-8><title>My Tickets preview</title><link rel=stylesheet href="/static/public/static/fonts.css"><link rel=stylesheet href="/my-tickets/assets/app.css"><body class="min-h-screen"><main class="mx-auto max-w-3xl px-4 pt-8"><div class="card" style="max-width:560px;margin:40px auto"><div class="card-header"><h1 class="card-title text-xl">My Tickets preview</h1><p class="card-description">Local UI preview with sample data. Pick who to sign in as. Each link mints a fresh token with that person's orgs and access, as core-accounts' /support page will. The tier is set for BigCorp, as an admin would on the Customer organisations page.</p></div><div class="card-content" style="display:grid;gap:8px">`)
	for _, o := range opts {
		fmt.Fprintf(&b, `<a class="btn btn-outline" style="justify-content:flex-start;white-space:normal;height:auto;padding:8px 14px;text-align:left" href="/preview-login?%s">%s</a>`, o.query, o.label)
	}
	b.WriteString(`</div></div></main></body>`)
	r.RequestCtx.SetContentType("text/html; charset=utf-8")
	r.RequestCtx.SetBodyString(b.String())
	return nil
}

type person struct {
	id, name, email string
	orgs            []map[string]any
}

func proj(id int, name, access string) map[string]any {
	return map[string]any{"id": id, "name": name, "access": access}
}

var people = map[string]person{
	"anita": {"1", "Anita Rao", "anita@bigcorp.com", []map[string]any{
		{"id": 42, "name": "BigCorp", "access": "manage", "projects": []map[string]any{proj(17, "Mobile app", "manage"), proj(18, "Website", "manage")}},
		{"id": 7, "name": "Acme", "access": "", "projects": []map[string]any{proj(31, "iOS app", "read")}}}},
	"ravi": {"2", "Ravi Kumar", "ravi@bigcorp.com", []map[string]any{
		{"id": 42, "name": "BigCorp", "access": "read", "projects": []map[string]any{proj(17, "Mobile app", "read"), proj(18, "Website", "read")}}}},
	"meera": {"3", "Meera Shah", "meera@bigcorp.com", []map[string]any{
		{"id": 42, "name": "BigCorp", "access": "", "projects": []map[string]any{proj(17, "Mobile app", "manage"), proj(18, "Website", "read")}}}},
	"sam": {"9", "Sam Lee", "sam@acme.com", []map[string]any{
		{"id": 7, "name": "Acme", "access": "manage", "projects": []map[string]any{proj(31, "iOS app", "manage")}}}},
}

// previewLogin signs a token for the chosen person and redirects to the real login.
func previewLogin(r *fastglue.Request) error {
	q := r.RequestCtx.QueryArgs()
	p, ok := people[string(q.Peek("user"))]
	if !ok {
		p = people["anita"]
	}
	now := time.Now()
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "encatch-accounts-local", "instance": "local", "external_user_id": p.id, "email": p.email, "name": p.name,
		"orgs": p.orgs,
		"iat":  now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": fmt.Sprintf("p-%d", now.UnixNano()),
	}).SignedString([]byte(secret))
	r.RequestCtx.Redirect("/my-tickets/login?token="+tok, http.StatusSeeOther)
	return nil
}

// ---- sample backend ----

type sampleBackend struct {
	mu       sync.Mutex
	contacts map[string]int
	names    map[int]string
	tickets  map[string]*mytickets.TicketSummary
	msgs     map[string][]mytickets.Message
	files    map[string]mytickets.Upload // uploaded attachments, served at /preview-uploads/{id}
	tiers    map[string]string           // org -> support tier (set on the Customer organisations page in the helpdesk)
	next     int
}

func (b *sampleBackend) setTier(orgID, tier string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tiers[orgID] = tier
}

func (b *sampleBackend) RegisterOrg(instance, orgID, orgName string) (string, error) {
	return b.OrgTier(orgID)
}

func (b *sampleBackend) LatestTicketOrg(contactID int, orgIDs []string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var best *mytickets.TicketSummary
	for _, t := range b.tickets {
		if t.ContactID != contactID {
			continue
		}
		for _, id := range orgIDs {
			if t.OrgID == id && (best == nil || t.CreatedAt.After(best.CreatedAt)) {
				best = t
			}
		}
	}
	if best == nil {
		return "", nil
	}
	return best.OrgID, nil
}

func (b *sampleBackend) OrgTier(orgID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.tiers[orgID]; ok {
		return t, nil
	}
	return "SaaS Standard", nil
}

func newSampleBackend() *sampleBackend {
	b := &sampleBackend{tiers: map[string]string{}, files: map[string]mytickets.Upload{}, contacts: map[string]int{"local-1": 1, "local-2": 2, "local-3": 3}, names: map[int]string{1: "Anita Rao", 2: "Ravi Kumar", 3: "Meera Shah", 4: "Kiran Das"},
		tickets: map[string]*mytickets.TicketSummary{}, msgs: map[string][]mytickets.Message{}, next: 140}
	ago := func(h int) time.Time { return time.Now().Add(-time.Duration(h) * time.Hour) }
	add := func(ref string, contact int, subject, status, project, projectName string, created, updated time.Time, msgs ...mytickets.Message) {
		b.tickets[ref] = &mytickets.TicketSummary{UUID: "u" + ref, ReferenceNumber: ref, Subject: subject, InternalStatus: status,
			ContactID: contact, RaisedBy: b.names[contact], OrgID: "local-42", ProjectID: project, ProjectName: projectName, CreatedAt: created, UpdatedAt: updated}
		b.msgs["u"+ref] = msgs
	}
	cust := func(who, html string, h int) mytickets.Message {
		return mytickets.Message{FromCustomer: true, AuthorName: who, HTML: html, CreatedAt: ago(h)}
	}
	agent := func(who, html string, h int) mytickets.Message {
		return mytickets.Message{AuthorName: who, HTML: html, CreatedAt: ago(h)}
	}
	add("131", 1, "Survey not showing on iOS 18 after SDK update", "Waiting on customer", "local-17", "Mobile app", ago(50), ago(3),
		cust("Anita Rao", "<p>Since updating the iOS SDK to 2.4.1, our in-app survey no longer appears. Android is fine.</p><p>Steps: open app, complete onboarding, survey should trigger on screen 3.</p>", 50),
		agent("Rahul Gorad", "<p>Hi Anita, thanks for the details. Could you share your <strong>workspace ID</strong> and the form ID? A short screen recording would also help.</p>", 3))
	add("128", 1, "How do I target users by page URL?", "Resolved", "local-18", "Website", ago(120), ago(96),
		cust("Anita Rao", "<p>We want the NPS survey only on /pricing. Is that possible without code?</p>", 120),
		agent("Saurav Choudhary", "<p>Yes. In the form's <em>Triggers</em>, add a <strong>Page visit</strong> rule with URL contains <code>/pricing</code>. No code change needed.</p>", 100),
		cust("Anita Rao", "<p>Works perfectly, thanks!</p>", 96))
	add("135", 2, "Webhook destination returning 401", "Waiting on engineering", "local-17", "Mobile app", ago(20), ago(5),
		cust("Ravi Kumar", "<p>Our webhook destination started failing with 401 since yesterday. Signing secret unchanged.</p>", 20),
		agent("Akash Patel", "<p>Thanks Ravi, we've reproduced this and passed it to engineering. We'll update you as soon as there's a fix.</p>", 5))
	add("137", 3, "Invoice address change", "Open", "", "", ago(4), ago(4),
		cust("Meera Shah", "<p>Please update our billing address on future invoices.</p>", 4))
	add("139", 2, "Export responses to CSV is slow", "Open", "local-18", "Website", ago(2), ago(1),
		cust("Ravi Kumar", "<p>CSV export for our largest form takes several minutes. Is there a faster way?</p>", 2))
	// Acme (org 7): one ticket in its iOS app project, raised by Acme's Kiran.
	b.tickets["140"] = &mytickets.TicketSummary{UUID: "u140", ReferenceNumber: "140", Subject: "Survey targeting on iPad", InternalStatus: "Open",
		ContactID: 4, RaisedBy: "Kiran Das", OrgID: "local-7", ProjectID: "local-31", ProjectName: "iOS app", CreatedAt: ago(30), UpdatedAt: ago(6)}
	b.msgs["u140"] = []mytickets.Message{cust("Kiran Das", "<p>Our NPS survey shows on iPhone but not on iPad. Same SDK version.</p>", 30)}
	return b
}

func (b *sampleBackend) ResolveContact(ext, email, first, last string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id, ok := b.contacts[ext]; ok {
		return id, nil
	}
	id := 100 + len(b.contacts)
	b.contacts[ext] = id
	b.names[id] = strings.TrimSpace(first + " " + last)
	return id, nil
}

func (b *sampleBackend) ListTickets(q mytickets.ListQuery) ([]mytickets.TicketSummary, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := mytickets.Session{ContactID: q.ContactID, OrgID: q.OrgID, Scope: q.Scope}
	for _, id := range q.ProjectIDs {
		s.Projects = append(s.Projects, mytickets.Project{ID: mytickets.ID(id)})
	}
	var out []mytickets.TicketSummary
	for _, t := range b.tickets {
		if !mytickets.Visible(s, *t) || (q.ProjectID != "" && t.ProjectID != q.ProjectID) ||
			(q.Status != "" && mytickets.CustomerStatus(t.InternalStatus) != q.Status) ||
			(q.Search != "" && strings.TrimPrefix(q.Search, "#") != t.ReferenceNumber &&
				!strings.Contains(strings.ToLower(t.Subject), strings.ToLower(q.Search))) {
			continue
		}
		row := *t
		if msgs := b.msgs[t.UUID]; len(msgs) > 0 {
			last := msgs[len(msgs)-1]
			row.UpdatedAt, row.LastFromCustomer, row.LastAuthor = last.CreatedAt, last.FromCustomer, last.AuthorName
			if last.FromCustomer {
				row.LastAuthorID = b.idOf(last.AuthorName)
			} else {
				row.LastAuthorID = -1
			}
			row.Preview = mytickets.Clip(html.UnescapeString(strict.Sanitize(strings.ReplaceAll(last.HTML, "</p>", "</p> "))))
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	out = out[min(q.Offset, len(out)):]
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// keep stores uploaded files in memory and returns their attachment links.
func (b *sampleBackend) keep(files []mytickets.Upload) []mytickets.Attachment {
	var out []mytickets.Attachment
	for _, f := range files {
		b.next++
		id := fmt.Sprint(b.next)
		b.files[id] = f
		out = append(out, mytickets.Attachment{Name: f.Name, URL: "/preview-uploads/" + id})
		log.Printf("preview: stored attachment %q (%d bytes)", f.Name, len(f.Data))
	}
	return out
}

// serveUpload returns a stored attachment as a download.
func (b *sampleBackend) serveUpload(r *fastglue.Request) error {
	b.mu.Lock()
	f, ok := b.files[r.RequestCtx.UserValue("id").(string)]
	b.mu.Unlock()
	if !ok {
		r.RequestCtx.SetStatusCode(fasthttp.StatusNotFound)
		return nil
	}
	r.RequestCtx.Response.Header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", f.Name))
	r.RequestCtx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	r.RequestCtx.SetContentType("application/octet-stream")
	r.RequestCtx.SetBody(f.Data)
	return nil
}

// strict strips sample message HTML down to text for list previews.
var strict = bluemonday.StrictPolicy()

// idOf finds a sample contact by name (the sample messages store names only).
func (b *sampleBackend) idOf(name string) int {
	for id, n := range b.names {
		if n == name {
			return id
		}
	}
	return 0
}

func (b *sampleBackend) GetTicket(ref string) (mytickets.TicketSummary, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.tickets[ref]
	if !ok {
		return mytickets.TicketSummary{}, errors.New("not found")
	}
	return *t, nil
}

// SyncNames: the preview keeps the names its sample tickets were made with.
func (b *sampleBackend) SyncNames(mytickets.Org) error { return nil }

func (b *sampleBackend) Messages(uuid string, page, perPage int) ([]mytickets.Message, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	all := b.msgs[uuid]
	return append([]mytickets.Message(nil), mytickets.PageMessages(all, page, perPage)...), len(all), nil
}

func (b *sampleBackend) CreateTicket(contactID int, subject, html string, attrs map[string]any, tags []string, files []mytickets.Upload) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	ref := fmt.Sprint(b.next)
	now := time.Now()
	pid, _ := attrs[mytickets.AttrProjectID].(string)
	pname, _ := attrs[mytickets.AttrProjectName].(string)
	b.tickets[ref] = &mytickets.TicketSummary{UUID: "u" + ref, ReferenceNumber: ref, Subject: subject + " - #" + ref, InternalStatus: "Open",
		ContactID: contactID, RaisedBy: b.names[contactID], OrgID: fmt.Sprint(attrs[mytickets.AttrOrgID]), ProjectID: pid, ProjectName: pname, CreatedAt: now, UpdatedAt: now}
	b.msgs["u"+ref] = []mytickets.Message{{FromCustomer: true, AuthorName: b.names[contactID], HTML: html, CreatedAt: now, Attachments: b.keep(files)}}
	log.Printf("preview: created #%s with attrs %v", ref, attrs)
	return ref, nil
}

func (b *sampleBackend) AddReply(contactID int, uuid, html string, files []mytickets.Upload) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs[uuid] = append(b.msgs[uuid], mytickets.Message{FromCustomer: true, AuthorName: b.names[contactID], HTML: html, CreatedAt: time.Now(), Attachments: b.keep(files)})
	for _, t := range b.tickets {
		if t.UUID == uuid {
			t.UpdatedAt = time.Now()
			if t.InternalStatus == "Waiting on customer" || t.InternalStatus == "Resolved" {
				t.InternalStatus = "Open"
			}
		}
	}
	return nil
}

func repoRoot() string {
	wd, _ := os.Getwd()
	for dir := wd; dir != "/"; dir = dir[:strings.LastIndex(dir, "/")] {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		if !strings.Contains(dir[1:], "/") {
			break
		}
	}
	return wd
}
