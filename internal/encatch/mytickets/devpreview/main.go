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
		Verifier: &mytickets.Verifier{Secrets: map[string][]string{"preview": {secret}},
			MaxLifetime: time.Minute, Leeway: 30 * time.Second, ValidTiers: tiers},
		Store:         mytickets.NewStore(rdb, 8*time.Hour),
		Backend:       backend,
		EligibleTiers: []string{"Growth Plus", "Enterprise Standard", "Enterprise Premium"},
		SessionTTL:    8 * time.Hour,
		Logger:        &lo,
		UploadLimits: func() mytickets.UploadLimits {
			// Same customer allowlist as deploy/config.toml (my_tickets.allowed_extensions).
			return mytickets.UploadLimits{MaxMB: 10, Extensions: []string{"png", "jpg", "jpeg", "gif", "webp", "heic", "pdf", "txt", "log",
				"csv", "json", "xml", "md", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "zip", "mp4", "mov", "webm", "mp3", "m4a", "wav", "har"}}
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	g := fastglue.NewGlue()
	g.GET("/", index)
	g.GET("/preview-login", previewLogin)
	g.GET("/my-tickets/assets/{file}", svc.Asset)
	g.GET("/my-tickets/login", svc.Login)
	g.POST("/my-tickets/logout", svc.Logout)
	g.GET("/my-tickets", svc.List)
	g.GET("/my-tickets/new", svc.NewForm)
	g.POST("/my-tickets/new", svc.Create)
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

// index lists one-click sign-ins for each scope and tier.
func index(r *fastglue.Request) error {
	type opt struct{ label, query string }
	opts := []opt{
		{"Anita (Growth Plus, self): sees only her tickets, can pick priority", "user=anita&tier=Growth+Plus&scope=self"},
		{"Anita (Growth Plus, project): sees Mobile app tickets from her team", "user=anita&tier=Growth+Plus&scope=project"},
		{"Ravi (Enterprise Premium, org): sees every BigCorp ticket", "user=ravi&tier=Enterprise+Premium&scope=org"},
		{"Meera (SaaS Standard, self): no priority choice", "user=meera&tier=SaaS+Standard&scope=self"},
		{"New user with no tickets yet (SaaS Growth)", "user=new&tier=SaaS+Growth&scope=self"},
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset=utf-8><title>My Tickets preview</title><link rel=stylesheet href="/static/public/static/fonts.css"><link rel=stylesheet href="/my-tickets/assets/app.css"><body class="min-h-screen"><main class="mx-auto max-w-3xl px-4 pt-8"><div class="card" style="max-width:560px;margin:40px auto"><div class="card-header"><h1 class="card-title text-xl">My Tickets preview</h1><p class="card-description">Local UI preview with sample data. Pick who to sign in as. Each link mints a fresh signed token, exactly like an Encatch backend app would.</p></div><div class="card-content" style="display:grid;gap:8px">`)
	for _, o := range opts {
		fmt.Fprintf(&b, `<a class="btn btn-outline" style="justify-content:flex-start;white-space:normal;height:auto;padding:8px 14px;text-align:left" href="/preview-login?%s">%s</a>`, o.query, o.label)
	}
	b.WriteString(`</div></div></main></body>`)
	r.RequestCtx.SetContentType("text/html; charset=utf-8")
	r.RequestCtx.SetBodyString(b.String())
	return nil
}

var people = map[string]struct{ id, name, email string }{
	"anita": {"1", "Anita Rao", "anita@bigcorp.com"},
	"ravi":  {"2", "Ravi Kumar", "ravi@bigcorp.com"},
	"meera": {"3", "Meera Shah", "meera@bigcorp.com"},
	"new":   {"9", "Sam New", "sam@bigcorp.com"},
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
		"iss": "preview", "external_user_id": p.id, "email": p.email, "name": p.name,
		"org_id": 42, "org_name": "BigCorp", "support_tier": string(q.Peek("tier")), "scope": string(q.Peek("scope")),
		"projects":           []map[string]any{{"id": 17, "name": "Mobile app"}, {"id": 18, "name": "Website"}},
		"current_project_id": 17,
		"iat":                now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": fmt.Sprintf("p-%d", now.UnixNano()),
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
	next     int
}

func newSampleBackend() *sampleBackend {
	b := &sampleBackend{files: map[string]mytickets.Upload{}, contacts: map[string]int{"1": 1, "2": 2, "3": 3}, names: map[int]string{1: "Anita Rao", 2: "Ravi Kumar", 3: "Meera Shah"},
		tickets: map[string]*mytickets.TicketSummary{}, msgs: map[string][]mytickets.Message{}, next: 140}
	ago := func(h int) time.Time { return time.Now().Add(-time.Duration(h) * time.Hour) }
	add := func(ref string, contact int, subject, status, project, projectName string, created, updated time.Time, msgs ...mytickets.Message) {
		b.tickets[ref] = &mytickets.TicketSummary{UUID: "u" + ref, ReferenceNumber: ref, Subject: subject, InternalStatus: status,
			ContactID: contact, RaisedBy: b.names[contact], OrgID: "42", ProjectID: project, ProjectName: projectName, CreatedAt: created, UpdatedAt: updated}
		b.msgs["u"+ref] = msgs
	}
	cust := func(who, html string, h int) mytickets.Message {
		return mytickets.Message{FromCustomer: true, AuthorName: who, HTML: html, CreatedAt: ago(h)}
	}
	agent := func(who, html string, h int) mytickets.Message {
		return mytickets.Message{AuthorName: who, HTML: html, CreatedAt: ago(h)}
	}
	add("131", 1, "Survey not showing on iOS 18 after SDK update", "Waiting on customer", "17", "Mobile app", ago(50), ago(3),
		cust("Anita Rao", "<p>Since updating the iOS SDK to 2.4.1, our in-app survey no longer appears. Android is fine.</p><p>Steps: open app, complete onboarding, survey should trigger on screen 3.</p>", 50),
		agent("Rahul Gorad", "<p>Hi Anita, thanks for the details. Could you share your <strong>workspace ID</strong> and the form ID? A short screen recording would also help.</p>", 3))
	add("128", 1, "How do I target users by page URL?", "Resolved", "18", "Website", ago(120), ago(96),
		cust("Anita Rao", "<p>We want the NPS survey only on /pricing. Is that possible without code?</p>", 120),
		agent("Saurav Choudhary", "<p>Yes. In the form's <em>Triggers</em>, add a <strong>Page visit</strong> rule with URL contains <code>/pricing</code>. No code change needed.</p>", 100),
		cust("Anita Rao", "<p>Works perfectly, thanks!</p>", 96))
	add("135", 2, "Webhook destination returning 401", "Waiting on engineering", "17", "Mobile app", ago(20), ago(5),
		cust("Ravi Kumar", "<p>Our webhook destination started failing with 401 since yesterday. Signing secret unchanged.</p>", 20),
		agent("Akash Patel", "<p>Thanks Ravi, we've reproduced this and passed it to engineering. We'll update you as soon as there's a fix.</p>", 5))
	add("137", 3, "Invoice address change", "Open", "", "", ago(4), ago(4),
		cust("Meera Shah", "<p>Please update our billing address on future invoices.</p>", 4))
	add("139", 2, "Export responses to CSV is slow", "Open", "18", "Website", ago(2), ago(1),
		cust("Ravi Kumar", "<p>CSV export for our largest form takes several minutes. Is there a faster way?</p>", 2))
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

func (b *sampleBackend) Messages(uuid string) ([]mytickets.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]mytickets.Message(nil), b.msgs[uuid]...), nil
}

func (b *sampleBackend) CreateTicket(contactID int, subject, html string, attrs map[string]any, files []mytickets.Upload) (string, error) {
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
