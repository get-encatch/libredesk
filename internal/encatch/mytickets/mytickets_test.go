package mytickets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"github.com/zerodha/logf"
)

var tiers = []string{"SaaS Standard", "SaaS Growth", "Growth Plus", "Enterprise Standard", "Enterprise Premium"}

func testVerifier() *Verifier {
	return &Verifier{
		Secrets:     map[string][]string{"encatch_accounts_prod": {"current-secret", "old-secret"}},
		MaxLifetime: 60 * time.Second, Leeway: 30 * time.Second,
	}
}

func sign(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func baseClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss": "encatch-accounts-prod", "instance": "prod", "external_user_id": 9134, "email": "Anita@BigCorp.com", "name": "Anita Rao",
		// Anita manages BigCorp's tickets (org_tickets:2).
		"orgs": []map[string]any{{"id": 42, "name": "BigCorp", "access": "manage",
			"projects": []map[string]any{{"id": 17, "name": "Mobile app", "access": "manage"}, {"id": "18", "name": "Website", "access": "read"}}}},
		"iat": now.Unix(), "exp": now.Add(60 * time.Second).Unix(), "jti": fmt.Sprintf("j-%d", now.UnixNano()),
	}
}

func TestVerify(t *testing.T) {
	v := testVerifier()
	mod := func(f func(jwt.MapClaims)) jwt.MapClaims { c := baseClaims(); f(c); return c }

	c, err := v.Verify(sign(t, "current-secret", baseClaims()))
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if c.Email != "anita@bigcorp.com" || c.ExternalUserID != "prod-9134" || c.Orgs[0].ID != "prod-42" ||
		c.Orgs[0].Projects[1].ID != "prod-18" || c.Orgs[0].Projects[1].Access != AccessManage { // org managers manage every project
		t.Fatalf("claims not normalised: %+v", c)
	}
	if _, err := v.Verify(sign(t, "old-secret", baseClaims())); err != nil {
		t.Fatalf("previous secret should still verify during rotation: %v", err)
	}

	bad := map[string]string{
		"wrong secret":   sign(t, "nope", baseClaims()),
		"unknown issuer": sign(t, "current-secret", mod(func(c jwt.MapClaims) { c["iss"] = "someone-else" })),
		"expired": sign(t, "current-secret", mod(func(c jwt.MapClaims) {
			c["iat"] = time.Now().Add(-5 * time.Minute).Unix()
			c["exp"] = time.Now().Add(-4 * time.Minute).Unix()
		})),
		"lifetime too long": sign(t, "current-secret", mod(func(c jwt.MapClaims) { c["exp"] = time.Now().Add(10 * time.Minute).Unix() })),
		"no jti":            sign(t, "current-secret", mod(func(c jwt.MapClaims) { delete(c, "jti") })),
		"no exp":            sign(t, "current-secret", mod(func(c jwt.MapClaims) { delete(c, "exp") })),
		"no orgs":           sign(t, "current-secret", mod(func(c jwt.MapClaims) { delete(c, "orgs") })),
		"bad email":         sign(t, "current-secret", mod(func(c jwt.MapClaims) { c["email"] = "nope" })),
		"bad org access":    sign(t, "current-secret", mod(func(c jwt.MapClaims) { c["orgs"] = []map[string]any{{"id": 1, "name": "X", "access": "admin"}} })),
		"garbage":           "not.a.jwt",
	}
	for name, tok := range bad {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s: token accepted, want rejection", name)
		}
	}

	none := jwt.NewWithClaims(jwt.SigningMethodNone, baseClaims())
	noneTok, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := v.Verify(noneTok); err == nil {
		t.Error("alg=none token accepted")
	}

}

func TestVerifyOrgs(t *testing.T) {
	v := testVerifier()
	tok := func(orgs []map[string]any) string {
		c := baseClaims()
		c["orgs"] = orgs
		return sign(t, "current-secret", c)
	}
	proj := func(id any, access string) map[string]any {
		return map[string]any{"id": id, "name": fmt.Sprint("P", id), "access": access}
	}

	// Orgs and projects without access are dropped; read stays read for project-only orgs.
	c, err := v.Verify(tok([]map[string]any{
		{"id": 1, "name": "Nothing here", "access": "", "projects": []map[string]any{proj(5, "")}},
		{"id": 2, "name": "Projects only", "access": "", "projects": []map[string]any{proj(6, "read"), proj(7, "manage"), proj(8, "")}},
		{"id": 3, "name": "Org reader", "access": "read", "projects": []map[string]any{proj(9, "manage")}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Orgs) != 2 || c.Orgs[0].ID != "prod-2" || len(c.Orgs[0].Projects) != 2 || c.Orgs[0].Projects[0].Access != AccessRead ||
		c.Orgs[1].Projects[0].Access != AccessManage { // org read + project manage: manage on that project
		t.Fatalf("orgs = %+v", c.Orgs)
	}

	many := make([]map[string]any, MaxProjects+1)
	for i := range many {
		many[i] = proj(i+1, "read")
	}
	for name, orgs := range map[string][]map[string]any{
		"no access anywhere":   {{"id": 1, "name": "X", "access": "", "projects": []map[string]any{proj(5, "")}}},
		"duplicate org":        {{"id": 1, "name": "X", "access": "read"}, {"id": 1, "name": "Y", "access": "read"}},
		"duplicate project":    {{"id": 1, "name": "X", "access": "", "projects": []map[string]any{proj(5, "read"), proj(5, "manage")}}},
		"bad project access":   {{"id": 1, "name": "X", "access": "", "projects": []map[string]any{proj(5, "owner")}}},
		"org without name":     {{"id": 1, "name": " ", "access": "read"}},
		"too many projects":    {{"id": 1, "name": "X", "access": "read", "projects": many}},
		"already prefixed org": {{"id": "prod-1", "name": "X", "access": "read"}},
		"non-numeric project":  {{"id": 1, "name": "X", "access": "", "projects": []map[string]any{proj("x1", "read")}}},
	} {
		if _, err := v.Verify(tok(orgs)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := v.Verify(tok([]map[string]any{{"id": 1, "name": "X", "access": ""}})); !errors.Is(err, ErrNoAccess) {
		t.Errorf("no access: err = %v, want ErrNoAccess", err)
	}
}

func TestCustomerStatus(t *testing.T) {
	cases := map[string]string{
		"Open": StatusOpen, "Snoozed": StatusOpen, "Waiting on engineering": StatusOpen,
		"Waiting on customer": StatusWaitingYou, "Resolved": StatusResolved, "Closed": StatusResolved,
	}
	for in, want := range cases {
		if got := CustomerStatus(in); got != want {
			t.Errorf("CustomerStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVisible(t *testing.T) {
	projects := []Project{{ID: "17", Name: "Mobile app", Access: AccessManage}}
	org := Session{ContactID: 1, OrgID: "42", OrgAccess: AccessRead, Scope: ScopeOrg}
	proj := Session{ContactID: 1, OrgID: "42", Scope: ScopeProject, Projects: projects}

	check := func(name string, s Session, tk TicketSummary, want bool) {
		if got := Visible(s, tk); got != want {
			t.Errorf("%s: Visible = %v, want %v", name, got, want)
		}
	}
	check("org sees teammate's project ticket", org, TicketSummary{ContactID: 2, OrgID: "42", ProjectID: "99"}, true)
	check("org sees org-level ticket", org, TicketSummary{ContactID: 2, OrgID: "42"}, true)
	check("org never crosses orgs", org, TicketSummary{ContactID: 1, OrgID: "7", ProjectID: "17"}, false)
	check("org never sees email tickets", org, TicketSummary{ContactID: 1}, false)
	check("project sees teammate in project", proj, TicketSummary{ContactID: 2, OrgID: "42", ProjectID: "17"}, true)
	check("project never sees other project", proj, TicketSummary{ContactID: 1, OrgID: "42", ProjectID: "99"}, false)
	check("project never sees org-level tickets, even own", proj, TicketSummary{ContactID: 1, OrgID: "42"}, false)
	check("project never crosses orgs", proj, TicketSummary{ContactID: 3, OrgID: "7", ProjectID: "17"}, false)
	check("project never sees email tickets", proj, TicketSummary{ContactID: 1}, false)
	check("no scope sees nothing", Session{OrgID: "42"}, TicketSummary{ContactID: 1, OrgID: "42", ProjectID: "17"}, false)
}

func TestScopeWhere(t *testing.T) {
	where, args := scopeWhere(ListQuery{Scope: ScopeProject, ContactID: 5, OrgID: "42", ProjectIDs: []string{"17"}, Status: StatusResolved})
	if !strings.Contains(where, "'org_id' = $1") || !strings.Contains(where, "'project_id' = ANY($2)") || strings.Contains(where, "contact_id") || len(args) != 2 {
		t.Fatalf("project where = %q %v", where, args)
	}
	where, _ = scopeWhere(ListQuery{Scope: ScopeOrg, OrgID: "42", ProjectID: "17"})
	if strings.Contains(where, "contact_id") || !strings.Contains(where, "'project_id' = $2") {
		t.Fatalf("org where = %q", where)
	}
	if where, _ = scopeWhere(ListQuery{Scope: "", OrgID: "42"}); !strings.Contains(where, "FALSE") {
		t.Fatalf("unknown scope must match nothing: %q", where)
	}
}

func TestScopeWhereSearch(t *testing.T) {
	where, args := scopeWhere(ListQuery{Scope: ScopeOrg, OrgID: "42", Search: "#131"})
	if !strings.Contains(where, "c.reference_number = $2 OR c.subject ILIKE $3") || args[1] != "131" || args[2] != "%#131%" {
		t.Fatalf("number search = %q %v", where, args)
	}
	where, args = scopeWhere(ListQuery{Scope: ScopeOrg, OrgID: "42", Search: `50%_off\`})
	if strings.Contains(where, "reference_number") || args[1] != `%50\%\_off\\%` {
		t.Fatalf("text search = %q %v", where, args)
	}
}

func TestTextToHTML(t *testing.T) {
	got := textToHTML("Hello <script>x</script>\nline two\n\nPara 2")
	want := "<p>Hello &lt;script&gt;x&lt;/script&gt;<br>line two</p><p>Para 2</p>"
	if got != want {
		t.Fatalf("textToHTML = %q, want %q", got, want)
	}
}

// ---- end-to-end handler flow with a fake backend ----

type fakeBackend struct {
	contacts map[string]int
	tickets  map[string]TicketSummary
	attrs    map[string]map[string]any
	tags     map[string][]string
	synced   []Org
	msgs     map[string][]Message
	files    []Upload
	tiers    map[string]string // org -> tier set by an admin
	seenOrgs map[string]string // org -> name, from RegisterOrg
}

func (f *fakeBackend) RegisterOrg(instance, orgID, orgName string) (string, error) {
	f.seenOrgs[orgID] = orgName
	return f.OrgTier(orgID)
}
func (f *fakeBackend) LatestTicketOrg(contactID int, orgIDs []string) (string, error) {
	var best TicketSummary
	for _, t := range f.tickets {
		if t.ContactID == contactID && contains(orgIDs, t.OrgID) && (best.OrgID == "" || t.CreatedAt.After(best.CreatedAt)) {
			best = t
		}
	}
	return best.OrgID, nil
}
func (f *fakeBackend) OrgTier(orgID string) (string, error) {
	if t, ok := f.tiers[orgID]; ok {
		return t, nil
	}
	return "SaaS Standard", nil
}

func newFake() *fakeBackend {
	return &fakeBackend{contacts: map[string]int{}, tickets: map[string]TicketSummary{}, attrs: map[string]map[string]any{}, tags: map[string][]string{}, msgs: map[string][]Message{},
		tiers: map[string]string{"prod-42": "Growth Plus"}, seenOrgs: map[string]string{}}
}

func (f *fakeBackend) ResolveContact(ext, email, first, last string) (int, error) {
	if id, ok := f.contacts[ext]; ok {
		return id, nil
	}
	f.contacts[ext] = len(f.contacts) + 1
	return f.contacts[ext], nil
}
func (f *fakeBackend) ListTickets(q ListQuery) ([]TicketSummary, error) {
	s := Session{ContactID: q.ContactID, OrgID: q.OrgID, Scope: q.Scope}
	for _, id := range q.ProjectIDs {
		s.Projects = append(s.Projects, Project{ID: ID(id)})
	}
	var out []TicketSummary
	for _, t := range f.tickets {
		if Visible(s, t) {
			out = append(out, t)
		}
	}
	// Newest first, like the adapter; then the requested page.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ReferenceNumber > out[j].ReferenceNumber
	})
	out = out[min(q.Offset, len(out)):]
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}
func (f *fakeBackend) SyncNames(org Org) error {
	f.synced = append(f.synced, org)
	names := map[string]string{}
	for _, p := range org.Projects {
		names[string(p.ID)] = p.Name
	}
	for ref, t := range f.tickets {
		if t.OrgID != string(org.ID) {
			continue
		}
		if n, ok := names[t.ProjectID]; ok {
			t.ProjectName = n
		}
		f.tickets[ref] = t
	}
	return nil
}
func (f *fakeBackend) GetTicket(ref string) (TicketSummary, error) {
	t, ok := f.tickets[ref]
	if !ok {
		return t, errors.New("not found")
	}
	return t, nil
}
func (f *fakeBackend) Messages(uuid string, page, perPage int) ([]Message, int, error) {
	return PageMessages(f.msgs[uuid], page, perPage), len(f.msgs[uuid]), nil
}
func (f *fakeBackend) CreateTicket(contactID int, subject, html string, attrs map[string]any, tags []string, files []Upload) (string, error) {
	ref := fmt.Sprint(100 + len(f.tickets))
	uuid := "u" + ref
	f.tickets[ref] = TicketSummary{UUID: uuid, ReferenceNumber: ref, Subject: subject, InternalStatus: "Open", ContactID: contactID,
		OrgID: fmt.Sprint(attrs[AttrOrgID]), ProjectID: fmt.Sprint(attrs[AttrProjectID]), RaisedBy: "Anita Rao"}
	f.attrs[ref] = attrs
	f.tags[ref] = tags
	f.msgs[uuid] = []Message{{FromCustomer: true, AuthorName: "Anita Rao", HTML: html + `<script>alert(1)</script>`}}
	f.files = append(f.files, files...)
	return ref, nil
}
func (f *fakeBackend) AddReply(contactID int, uuid, html string, files []Upload) error {
	f.msgs[uuid] = append(f.msgs[uuid], Message{FromCustomer: true, HTML: html})
	f.files = append(f.files, files...)
	return nil
}

type harness struct {
	t   *testing.T
	svc *Service
	be  *fakeBackend
}

func newHarness(t *testing.T) *harness {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	lo := logf.New(logf.Opts{Level: logf.ErrorLevel})
	be := newFake()
	svc, err := New(Opts{Verifier: testVerifier(), Store: NewStore(rdb, 8*time.Hour), Backend: be,
		EligibleTiers: []string{"Growth Plus", "Enterprise Standard", "Enterprise Premium"}, SessionTTL: 8 * time.Hour, Logger: &lo,
		UploadLimits: func() UploadLimits { return UploadLimits{MaxMB: 1, Extensions: []string{"pdf", "png", "txt"}} }})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, svc: svc, be: be}
}

// do runs a handler against a request and returns the response.
func (h *harness) do(handler func(*fastglue.Request) error, method, uri, cookie, form string, userValues map[string]string) *fasthttp.Response {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	if cookie != "" {
		ctx.Request.Header.SetCookie(cookieName, cookie)
	}
	if form != "" {
		ctx.Request.Header.SetContentType("application/x-www-form-urlencoded")
		ctx.Request.SetBodyString(form)
	}
	for k, v := range userValues {
		ctx.SetUserValue(k, v)
	}
	if err := handler(&fastglue.Request{RequestCtx: ctx}); err != nil {
		h.t.Fatalf("handler error: %v", err)
	}
	resp := &fasthttp.Response{}
	ctx.Response.CopyTo(resp)
	return resp
}

func sessionCookie(t *testing.T, resp *fasthttp.Response) string {
	c := fasthttp.AcquireCookie()
	c.SetKey(cookieName)
	if !resp.Header.Cookie(c) {
		t.Fatal("no session cookie set")
	}
	return string(c.Value())
}

func TestFlow(t *testing.T) {
	h := newHarness(t)
	tok := sign(t, "current-secret", baseClaims())

	resp := h.do(h.svc.Login, "GET", "/my-tickets/login?token="+tok, "", "", nil)
	if resp.StatusCode() != fasthttp.StatusSeeOther || string(resp.Header.Peek("Location")) != "/my-tickets" {
		t.Fatalf("login: status %d location %q body %s", resp.StatusCode(), resp.Header.Peek("Location"), resp.Body())
	}
	if string(resp.Header.Peek("Referrer-Policy")) != "no-referrer" {
		t.Fatal("login redirect must send Referrer-Policy: no-referrer")
	}
	sid := sessionCookie(t, resp)

	// Replaying the same token is refused.
	if resp := h.do(h.svc.Login, "GET", "/my-tickets/login?token="+tok, "", "", nil); resp.StatusCode() != fasthttp.StatusUnauthorized {
		t.Fatalf("replay: status %d, want 401", resp.StatusCode())
	}

	// Without a session, pages ask the customer to reopen Support.
	if resp := h.do(h.svc.List, "GET", "/my-tickets", "", "", nil); resp.StatusCode() != fasthttp.StatusUnauthorized {
		t.Fatalf("no session list: status %d", resp.StatusCode())
	}

	sess, _ := h.svc.store.Get(context.Background(), sid)
	csrf := sess.CSRF

	// Missing CSRF is refused.
	if resp := h.do(h.svc.Create, "POST", "/my-tickets/new", sid, "subject=Hi&message=Body", nil); resp.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("create without csrf: status %d", resp.StatusCode())
	}
	// A project the user can't see is refused.
	resp = h.do(h.svc.Create, "POST", "/my-tickets/new", sid, "csrf="+csrf+"&subject=Hi&message=Body&project=999", nil)
	if !strings.Contains(string(resp.Body()), "choose one of your projects") || len(h.be.tickets) != 0 {
		t.Fatalf("foreign project accepted: %s", resp.Body())
	}

	resp = h.do(h.svc.Create, "POST", "/my-tickets/new", sid, "csrf="+csrf+"&subject=Checkout+broken&message=Steps%0A1.+open&project=prod-17&priority=Urgent", nil)
	if resp.StatusCode() != fasthttp.StatusSeeOther || string(resp.Header.Peek("Location")) != "/my-tickets/100" {
		t.Fatalf("create: %d %q %s", resp.StatusCode(), resp.Header.Peek("Location"), resp.Body())
	}
	a := h.be.attrs["100"]
	if a[AttrOrgID] != "prod-42" || a[AttrTicketTier] != "Growth Plus" || a[AttrRequestedPriority] != "Urgent" ||
		a[AttrProjectID] != "prod-17" || a[AttrProjectName] != "Mobile app" || a[AttrSourceApp] != "encatch-accounts-prod" ||
		a[AttrInstance] != "prod" {
		t.Fatalf("ticket attrs = %v", a)
	}
	if tags := h.be.tags["100"]; len(tags) != 1 || tags[0] != "instance:prod" {
		t.Fatalf("ticket tags = %v", tags)
	}

	resp = h.do(h.svc.List, "GET", "/my-tickets", sid, "", nil)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), "Checkout broken") {
		t.Fatalf("list: %d %s", resp.StatusCode(), resp.Body())
	}

	resp = h.do(h.svc.View, "GET", "/my-tickets/100", sid, "", map[string]string{"ref": "100"})
	body := string(resp.Body())
	if resp.StatusCode() != 200 || !strings.Contains(body, "Steps") || strings.Contains(body, "<script>alert") {
		t.Fatalf("view: %d, sanitised=%v", resp.StatusCode(), !strings.Contains(body, "<script>alert"))
	}

	resp = h.do(h.svc.Reply, "POST", "/my-tickets/100/reply", sid, "csrf="+csrf+"&message=More+info", map[string]string{"ref": "100"})
	if resp.StatusCode() != fasthttp.StatusSeeOther || len(h.be.msgs["u100"]) != 2 {
		t.Fatalf("reply: %d, msgs %d", resp.StatusCode(), len(h.be.msgs["u100"]))
	}
	if loc := string(resp.Header.Peek("Location")); loc != "/my-tickets/100?sent=1" {
		t.Fatalf("reply location = %q", loc)
	}
	// The inbox filters survive a reply; the redirect stays on our own path.
	resp = h.do(h.svc.Reply, "POST", "/my-tickets/100/reply?status=Open&q=a+b&next=//evil.example", sid, "csrf="+csrf+"&message=Again", map[string]string{"ref": "100"})
	if loc := string(resp.Header.Peek("Location")); loc != "/my-tickets/100?q=a+b&sent=1&status=Open" {
		t.Fatalf("reply location with filters = %q", loc)
	}

	// Another org's ticket is hidden and returns 404, not 403.
	h.be.tickets["900"] = TicketSummary{UUID: "u900", ReferenceNumber: "900", ContactID: 1, OrgID: "prod-7", ProjectID: "prod-17"}
	if resp := h.do(h.svc.View, "GET", "/my-tickets/900", sid, "", map[string]string{"ref": "900"}); resp.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("other org's ticket: status %d", resp.StatusCode())
	}

	// The helpdesk owns the tier: the token's support_tier is ignored. An admin moved
	// the org to SaaS Standard after this user signed in; the next ticket uses it, and
	// SaaS Standard orgs can't request a priority.
	low := baseClaims()
	low["support_tier"] = "Enterprise Premium"
	low["jti"] = "low-1"
	sid2 := sessionCookie(t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "current-secret", low), "", "", nil))
	if h.be.seenOrgs["prod-42"] != "BigCorp" {
		t.Fatalf("org not registered on sign-in: %v", h.be.seenOrgs)
	}
	h.be.tiers["prod-42"] = "SaaS Standard"
	s2, _ := h.svc.store.Get(context.Background(), sid2)
	if body := string(h.do(h.svc.NewForm, "GET", "/my-tickets/new", sid2, "", nil).Body()); strings.Contains(body, `name="priority"`) {
		t.Fatal("SaaS Standard org was offered a priority choice")
	}
	h.do(h.svc.Create, "POST", "/my-tickets/new", sid2, "csrf="+s2.CSRF+"&subject=Q&message=Q&priority=Urgent", nil)
	if a := h.be.attrs["102"]; a[AttrTicketTier] != "SaaS Standard" || a[AttrRequestedPriority] != nil {
		t.Fatalf("ticket after tier change: %v", a)
	}

	// Orgs nobody has set a tier for get the default.
	delete(h.be.tiers, "prod-42")
	h.do(h.svc.Create, "POST", "/my-tickets/new", sid2, "csrf="+s2.CSRF+"&subject=Q2&message=Q", nil)
	if a := h.be.attrs["103"]; a[AttrTicketTier] != "SaaS Standard" {
		t.Fatalf("default tier: %v", a)
	}

	// Logout ends the session.
	h.do(h.svc.Logout, "POST", "/my-tickets/logout", sid, "csrf="+csrf, nil)
	if _, err := h.svc.store.Get(context.Background(), sid); !errors.Is(err, ErrNoSession) {
		t.Fatal("session still valid after logout")
	}
}

func TestAsset(t *testing.T) {
	h := newHarness(t)
	for file, ctype := range assetTypes {
		resp := h.do(h.svc.Asset, "GET", "/my-tickets/assets/"+file, "", "", map[string]string{"file": file})
		if resp.StatusCode() != 200 || string(resp.Header.ContentType()) != ctype || len(resp.Body()) == 0 {
			t.Errorf("%s: status %d, type %q, %d bytes", file, resp.StatusCode(), resp.Header.ContentType(), len(resp.Body()))
		}
	}
	for _, file := range []string{"../handlers.go", "app.css/", "missing.png", ""} {
		if resp := h.do(h.svc.Asset, "GET", "/my-tickets/assets/x", "", "", map[string]string{"file": file}); resp.StatusCode() != 404 {
			t.Errorf("%q: want 404, got %d", file, resp.StatusCode())
		}
	}
}

func TestInitials(t *testing.T) {
	for _, c := range []struct{ name, email, want string }{
		{"Anita Rao", "a@x.com", "AR"},
		{"ravi", "r@x.com", "R"},
		{"Anne Marie Smith", "", "AM"},
		{"", "meera@x.com", "M"},
		{"  ", "", ""},
		{"élodie durand", "", "ÉD"},
	} {
		if got := initials(c.name, c.email); got != c.want {
			t.Errorf("initials(%q, %q) = %q, want %q", c.name, c.email, got, c.want)
		}
	}
}

func TestSender(t *testing.T) {
	sess := Session{ContactID: 7}
	for _, c := range []struct {
		t    TicketSummary
		want string
	}{
		{TicketSummary{RaisedBy: "Anita Rao"}, "Anita Rao"},
		{TicketSummary{RaisedBy: "Anita Rao", LastAuthorID: 3, LastAuthor: "Rahul Gorad", Preview: "Hi"}, "Encatch Support"},
		{TicketSummary{RaisedBy: "Anita Rao", LastFromCustomer: true, LastAuthorID: 7, LastAuthor: "Ravi Kumar", Preview: "x"}, "You"},
		{TicketSummary{RaisedBy: "Anita Rao", LastFromCustomer: true, LastAuthorID: 9, LastAuthor: "Meera Shah", Preview: "x"}, "Meera Shah"},
		{TicketSummary{RaisedBy: "Anita Rao", LastFromCustomer: true, LastAuthorID: 9, Preview: "x"}, "Anita Rao"},
	} {
		if got := sender(sess, c.t); got != c.want {
			t.Errorf("sender(%+v) = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestMailDate(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, ist)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 10, 10, 8, 5, 0, 0, ist), "08:05"},
		{time.Date(2026, 10, 9, 23, 0, 0, 0, ist), "9 Oct"},
		{time.Date(2026, 10, 9, 19, 0, 0, 0, time.UTC), "00:30"}, // 00:30 IST on 10 Oct: today in IST
		{time.Date(2025, 12, 31, 12, 0, 0, 0, ist), "31 Dec 2025"},
	} {
		if got := mailDate(c.t, now); got != c.want {
			t.Errorf("mailDate(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestClip(t *testing.T) {
	if got := Clip("  Hello\n\n  world\t "); got != "Hello world" {
		t.Errorf("Clip whitespace = %q", got)
	}
	long := strings.Repeat("ab ", 200)
	got := Clip(long)
	if n := len([]rune(got)); n > maxPreview+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("Clip long: %d runes, %q", n, got[len(got)-10:])
	}
}

type testFile struct {
	field, name string
	data        []byte
}

// doMultipart posts a multipart form like a browser does.
func (h *harness) doMultipart(handler func(*fastglue.Request) error, uri, cookie string, fields map[string]string, files []testFile, userValues map[string]string) *fasthttp.Response {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for _, f := range files {
		part, _ := w.CreateFormFile(f.field, f.name)
		_, _ = part.Write(f.data)
	}
	_ = w.Close()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(uri)
	ctx.Request.Header.SetCookie(cookieName, cookie)
	ctx.Request.Header.SetContentType(w.FormDataContentType())
	ctx.Request.SetBody(buf.Bytes())
	for k, v := range userValues {
		ctx.SetUserValue(k, v)
	}
	if err := handler(&fastglue.Request{RequestCtx: ctx}); err != nil {
		h.t.Fatalf("handler error: %v", err)
	}
	resp := &fasthttp.Response{}
	ctx.Response.CopyTo(resp)
	return resp
}

func TestUploads(t *testing.T) {
	h := newHarness(t)
	resp := h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "current-secret", baseClaims()), "", "", nil)
	sid := sessionCookie(t, resp)
	sess, _ := h.svc.store.Get(context.Background(), sid)
	fields := map[string]string{"csrf": sess.CSRF, "subject": "Crash on export", "message": "See attached"}
	pdf := testFile{"files", "report.pdf", []byte("%PDF-1.4 test")}

	// The new-ticket form shows the file field with the allowed types.
	if body := string(h.do(h.svc.NewForm, "GET", "/my-tickets/new", sid, "", nil).Body()); !strings.Contains(body, `enctype="multipart/form-data"`) || !strings.Contains(body, `accept=".pdf,.png,.txt"`) {
		t.Fatalf("new form lacks the file field: %s", body)
	}

	// Rejected uploads keep the form and create nothing.
	for name, files := range map[string][]testFile{
		"type":  {{"files", "setup.exe", []byte("MZ")}},
		"size":  {{"files", "big.pdf", bytes.Repeat([]byte("a"), 1<<20+1)}},
		"count": {pdf, pdf, pdf, pdf, pdf, pdf},
		"empty": {{"files", "empty.txt", nil}},
	} {
		resp := h.doMultipart(h.svc.Create, "/my-tickets/new", sid, fields, files, nil)
		if resp.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(resp.Body()), "alert-destructive") ||
			!strings.Contains(string(resp.Body()), "Crash on export") || len(h.be.tickets) != 0 {
			t.Fatalf("%s: status %d, tickets %d", name, resp.StatusCode(), len(h.be.tickets))
		}
	}

	// Valid files are passed to the backend; an unchosen file input (empty part) is ignored,
	// and a path in the filename is dropped.
	resp = h.doMultipart(h.svc.Create, "/my-tickets/new", sid, fields,
		[]testFile{pdf, {"files", "../../etc/notes.txt", []byte("hello")}, {"files", "", nil}}, nil)
	if resp.StatusCode() != fasthttp.StatusSeeOther || len(h.be.files) != 2 {
		t.Fatalf("create with files: %d, files %d, body %s", resp.StatusCode(), len(h.be.files), resp.Body())
	}
	if h.be.files[0].Name != "report.pdf" || string(h.be.files[0].Data) != "%PDF-1.4 test" || h.be.files[1].Name != "notes.txt" {
		t.Fatalf("stored files = %+v", h.be.files)
	}

	// A reply may be just a file.
	ref := strings.TrimPrefix(string(resp.Header.Peek("Location")), "/my-tickets/")
	reply := map[string]string{"csrf": sess.CSRF, "message": ""}
	resp = h.doMultipart(h.svc.Reply, "/my-tickets/"+ref+"/reply", sid, reply, []testFile{{"files", "shot.png", []byte("png")}}, map[string]string{"ref": ref})
	if resp.StatusCode() != fasthttp.StatusSeeOther || len(h.be.files) != 3 {
		t.Fatalf("file-only reply: %d, files %d", resp.StatusCode(), len(h.be.files))
	}

	// An empty reply, or a bad file, shows the problem on the ticket and keeps the draft.
	resp = h.doMultipart(h.svc.Reply, "/my-tickets/"+ref+"/reply", sid, reply, nil, map[string]string{"ref": ref})
	if !strings.Contains(string(resp.Body()), "Please write a reply or attach a file.") {
		t.Fatalf("empty reply: %d %s", resp.StatusCode(), resp.Body())
	}
	reply["message"] = "Keep this draft"
	resp = h.doMultipart(h.svc.Reply, "/my-tickets/"+ref+"/reply", sid, reply, []testFile{{"files", "x.exe", []byte("MZ")}}, map[string]string{"ref": ref})
	if body := string(resp.Body()); !strings.Contains(body, "file type isn") || !strings.Contains(body, ">Keep this draft</textarea>") || len(h.be.files) != 3 {
		t.Fatalf("bad file reply: %d files %d %s", resp.StatusCode(), len(h.be.files), body)
	}

	// Multipart posts still need the CSRF token.
	if resp := h.doMultipart(h.svc.Reply, "/my-tickets/"+ref+"/reply", sid, map[string]string{"message": "x"}, []testFile{pdf}, map[string]string{"ref": ref}); resp.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("multipart without csrf: %d", resp.StatusCode())
	}
}

func TestIntersectExtensions(t *testing.T) {
	for _, c := range []struct {
		ld, cu, want []string
	}{
		{[]string{"*"}, []string{"PDF", ".png"}, []string{"pdf", "png"}},
		{[]string{"pdf", "exe"}, []string{"pdf", "png"}, []string{"pdf"}},
		{[]string{"pdf"}, nil, []string{"pdf"}},
		{[]string{"*"}, nil, []string{"*"}},
		{[]string{"zip"}, []string{"pdf"}, nil},
	} {
		if got := IntersectExtensions(c.ld, c.cu); !slices.Equal(got, c.want) {
			t.Errorf("IntersectExtensions(%v, %v) = %v, want %v", c.ld, c.cu, got, c.want)
		}
	}
}

func TestSecretsWarning(t *testing.T) {
	h := newHarness(t)
	sid := sessionCookie(t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "current-secret", baseClaims()), "", "", nil))
	if body := string(h.do(h.svc.NewForm, "GET", "/my-tickets/new", sid, "", nil).Body()); !strings.Contains(body, "share passwords or secrets") {
		t.Fatal("new-ticket form lacks the secrets warning")
	}
	h.be.tickets["500"] = TicketSummary{UUID: "u500", ReferenceNumber: "500", ContactID: 1, OrgID: "prod-42", InternalStatus: "Open"}
	if body := string(h.do(h.svc.View, "GET", "/my-tickets/500", sid, "", map[string]string{"ref": "500"}).Body()); !strings.Contains(body, "include passwords, API keys or tokens") {
		t.Fatal("reply box lacks the secrets reminder")
	}
}

func TestUploadTotalLimit(t *testing.T) {
	h := newHarness(t)
	h.svc.uploadLimits = func() UploadLimits {
		return UploadLimits{MaxMB: 1, MaxTotalMB: 1, Extensions: []string{"txt"}}
	}
	sid := sessionCookie(t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "current-secret", baseClaims()), "", "", nil))
	sess, _ := h.svc.store.Get(context.Background(), sid)
	if body := string(h.do(h.svc.NewForm, "GET", "/my-tickets/new", sid, "", nil).Body()); !strings.Contains(body, "1 MB each, 1 MB in total") {
		t.Fatal("new form doesn't show the total limit")
	}
	fields := map[string]string{"csrf": sess.CSRF, "subject": "Logs", "message": "Two logs attached"}
	half := bytes.Repeat([]byte("a"), 600<<10) // each file is under 1 MB, together over it
	resp := h.doMultipart(h.svc.Create, "/my-tickets/new", sid, fields, []testFile{{"files", "a.txt", half}, {"files", "b.txt", half}}, nil)
	if body := string(resp.Body()); !strings.Contains(body, "up to 1 MB in total") || len(h.be.tickets) != 0 {
		t.Fatalf("over the total: status %d, tickets %d", resp.StatusCode(), len(h.be.tickets))
	}
	resp = h.doMultipart(h.svc.Create, "/my-tickets/new", sid, fields, []testFile{{"files", "a.txt", half}}, nil)
	if resp.StatusCode() != fasthttp.StatusSeeOther || len(h.be.files) != 1 {
		t.Fatalf("within the total: status %d, files %d", resp.StatusCode(), len(h.be.files))
	}
	// No total shown when it can't limit anything beyond files x size.
	h.svc.uploadLimits = func() UploadLimits { return UploadLimits{MaxMB: 1, MaxTotalMB: 50, Extensions: []string{"txt"}} }
	if body := string(h.do(h.svc.NewForm, "GET", "/my-tickets/new", sid, "", nil).Body()); strings.Contains(body, "in total") {
		t.Fatal("total shown although it doesn't limit anything")
	}
}

func TestInstances(t *testing.T) {
	v := testVerifier()
	v.Secrets["encatch_accounts_uat"] = []string{"uat-secret"}
	v.Secrets["encatch_dashboard"] = []string{"current-secret"} // an old-style issuer
	mod := func(secret string, f func(jwt.MapClaims)) string {
		c := baseClaims()
		f(c)
		return sign(t, secret, c)
	}

	// The instance comes from the issuer and prefixes every id.
	c, err := v.Verify(mod("uat-secret", func(c jwt.MapClaims) {
		c["iss"], c["instance"], c["external_user_id"] = "encatch-accounts-uat", "uat", "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Orgs[0].ID != "uat-42" || c.Orgs[0].Projects[0].ID != "uat-17" ||
		c.ExternalUserID != "uat-0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" || c.Instance != "uat" {
		t.Fatalf("uat claims = %+v", c)
	}

	for name, tok := range map[string]string{
		"instance claim missing":  mod("current-secret", func(c jwt.MapClaims) { delete(c, "instance") }),
		"instance claim mismatch": mod("current-secret", func(c jwt.MapClaims) { c["instance"] = "uat" }),
		// uat's secret can't speak for prod, even when the token says prod.
		"other instance's secret": mod("uat-secret", func(c jwt.MapClaims) {}),
		"old-style issuer":        mod("current-secret", func(c jwt.MapClaims) { c["iss"] = "encatch-dashboard" }),
		"bad user id":             mod("current-secret", func(c jwt.MapClaims) { c["external_user_id"] = "a b" }),
	} {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	if inst, ok := InstanceOf("encatch-accounts-local"); !ok || inst != "local" {
		t.Errorf("InstanceOf(local) = %q, %v", inst, ok)
	}
	for _, iss := range []string{"encatch-accounts-", "encatch-accounts-PROD", "encatch-accounts-prod-eu", "other-prod"} {
		if _, ok := InstanceOf(iss); ok {
			t.Errorf("InstanceOf(%q) accepted", iss)
		}
	}
}

// Org 42 on uat and org 42 on prod are different customers: neither sees the other's tickets.
func TestInstanceIsolation(t *testing.T) {
	h := newHarness(t)
	h.svc.verifier.Secrets["encatch_accounts_uat"] = []string{"uat-secret"}
	c := baseClaims()
	c["iss"], c["instance"] = "encatch-accounts-uat", "uat"
	sid := sessionCookie(t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "uat-secret", c), "", "", nil))
	h.be.tickets["700"] = TicketSummary{UUID: "u700", ReferenceNumber: "700", ContactID: 50, OrgID: "prod-42", Subject: "Prod org ticket", InternalStatus: "Open"}
	h.be.tickets["701"] = TicketSummary{UUID: "u701", ReferenceNumber: "701", ContactID: 51, OrgID: "uat-42", Subject: "Uat org ticket", InternalStatus: "Open"}
	body := string(h.do(h.svc.List, "GET", "/my-tickets", sid, "", nil).Body())
	if strings.Contains(body, "Prod org ticket") || !strings.Contains(body, "Uat org ticket") {
		t.Fatalf("uat org-scope list leaked or missed tickets")
	}
	if resp := h.do(h.svc.View, "GET", "/my-tickets/700", sid, "", map[string]string{"ref": "700"}); resp.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("uat user opened a prod ticket: %d", resp.StatusCode())
	}
}

// signIn signs a token with the given orgs for user 9134 and returns the session id.
func (h *harness) signIn(orgs []map[string]any) (string, Session) {
	c := baseClaims()
	c["orgs"] = orgs
	c["jti"] = fmt.Sprintf("j-%d", time.Now().UnixNano())
	sid := sessionCookie(h.t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(h.t, "current-secret", c), "", "", nil))
	sess, _ := h.svc.store.Get(context.Background(), sid)
	return sid, sess
}

func TestAccess(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.be.tickets["200"] = TicketSummary{UUID: "u200", ReferenceNumber: "200", ContactID: 50, OrgID: "prod-42", Subject: "Org-level billing", InternalStatus: "Open", CreatedAt: now}
	h.be.tickets["201"] = TicketSummary{UUID: "u201", ReferenceNumber: "201", ContactID: 50, OrgID: "prod-42", ProjectID: "prod-17", ProjectName: "Mobile app", Subject: "Mobile crash", InternalStatus: "Open", CreatedAt: now}
	h.be.tickets["202"] = TicketSummary{UUID: "u202", ReferenceNumber: "202", ContactID: 50, OrgID: "prod-42", ProjectID: "prod-18", ProjectName: "Website", Subject: "Website slow", InternalStatus: "Open", CreatedAt: now}
	h.be.msgs["u201"] = []Message{{FromCustomer: true, HTML: "x"}}
	h.be.msgs["u202"] = []Message{{FromCustomer: true, HTML: "x"}}
	page := func(sid, path string, uv map[string]string) (int, string) {
		resp := h.do(map[string]func(*fastglue.Request) error{"list": h.svc.List, "new": h.svc.NewForm, "view": h.svc.View}[path], "GET", "/", sid, "", uv)
		return resp.StatusCode(), string(resp.Body())
	}

	t.Run("org reader: sees everything, raises and replies nothing", func(t *testing.T) {
		sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "read"}})
		if _, body := page(sid, "list", nil); !strings.Contains(body, "Org-level billing") || !strings.Contains(body, "Website slow") || strings.Contains(body, "New ticket") {
			t.Fatal("reader list: wrong tickets or a New ticket button")
		}
		if code, _ := page(sid, "new", nil); code != fasthttp.StatusForbidden {
			t.Fatalf("reader new form: %d", code)
		}
		if resp := h.do(h.svc.Create, "POST", "/my-tickets/new", sid, "csrf="+sess.CSRF+"&subject=S&message=M", nil); resp.StatusCode() != fasthttp.StatusForbidden {
			t.Fatalf("reader create: %d", resp.StatusCode())
		}
		if _, body := page(sid, "view", map[string]string{"ref": "201"}); strings.Contains(body, `name="message"`) || !strings.Contains(body, "read-only access") {
			t.Fatal("reader ticket page offers a reply box")
		}
		h.do(h.svc.Reply, "POST", "/my-tickets/201/reply", sid, "csrf="+sess.CSRF+"&message=hi", map[string]string{"ref": "201"})
		if len(h.be.msgs["u201"]) != 1 {
			t.Fatal("reader reply was added")
		}
	})

	t.Run("project manager: own projects only, no org-level", func(t *testing.T) {
		sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "", "projects": []map[string]any{
			{"id": 17, "name": "Mobile app", "access": "manage"}, {"id": 18, "name": "Website", "access": "read"}}}})
		if sess.Scope != ScopeProject {
			t.Fatalf("scope = %q", sess.Scope)
		}
		if _, body := page(sid, "list", nil); strings.Contains(body, "Org-level billing") || !strings.Contains(body, "Mobile crash") || !strings.Contains(body, "Website slow") {
			t.Fatal("project list: wrong tickets")
		}
		if code, _ := page(sid, "view", map[string]string{"ref": "200"}); code != fasthttp.StatusNotFound {
			t.Fatalf("project user opened an org-level ticket: %d", code)
		}
		_, form := page(sid, "new", nil)
		if strings.Contains(form, "Not about a specific project") || !strings.Contains(form, ">Mobile app</option>") || strings.Contains(form, ">Website</option>") {
			t.Fatal("new form offers org-level or a read-only project")
		}
		before := len(h.be.tickets)
		for _, project := range []string{"", "prod-18", "prod-99"} {
			resp := h.do(h.svc.Create, "POST", "/my-tickets/new", sid, "csrf="+sess.CSRF+"&subject=S&message=M&project="+project, nil)
			if !strings.Contains(string(resp.Body()), "choose one of your projects") {
				t.Fatalf("create in %q accepted: %d", project, resp.StatusCode())
			}
		}
		if resp := h.do(h.svc.Create, "POST", "/my-tickets/new", sid, "csrf="+sess.CSRF+"&subject=S&message=M&project=prod-17", nil); resp.StatusCode() != fasthttp.StatusSeeOther || len(h.be.tickets) != before+1 {
			t.Fatalf("create in managed project: %d", resp.StatusCode())
		}
		h.do(h.svc.Reply, "POST", "/my-tickets/202/reply", sid, "csrf="+sess.CSRF+"&message=hi", map[string]string{"ref": "202"})
		if len(h.be.msgs["u202"]) != 1 {
			t.Fatal("reply on a read-only project was added")
		}
		if resp := h.do(h.svc.Reply, "POST", "/my-tickets/201/reply", sid, "csrf="+sess.CSRF+"&message=hi", map[string]string{"ref": "201"}); resp.StatusCode() != fasthttp.StatusSeeOther {
			t.Fatalf("reply on a managed project: %d", resp.StatusCode())
		}
	})

	t.Run("two orgs: opens the latest ticket's org and switches", func(t *testing.T) {
		h.be.contacts["prod-9134"] = 77
		h.be.tickets["300"] = TicketSummary{UUID: "u300", ReferenceNumber: "300", ContactID: 77, OrgID: "prod-7", Subject: "Acme question", InternalStatus: "Open", CreatedAt: now.Add(time.Hour)}
		sid, sess := h.signIn([]map[string]any{
			{"id": 42, "name": "BigCorp", "access": "read"},
			{"id": 7, "name": "Acme", "access": "manage"}})
		if sess.OrgID != "prod-7" {
			t.Fatalf("opened %q, want the latest ticket's org prod-7", sess.OrgID)
		}
		_, body := page(sid, "list", nil)
		if !strings.Contains(body, "Acme question") || strings.Contains(body, "Mobile crash") || !strings.Contains(body, `value="prod-42"`) {
			t.Fatal("Acme list wrong, or no switcher")
		}
		ttl := h.svc.store.rdb.TTL(context.Background(), sessionPrefix+sid).Val()
		if resp := h.do(h.svc.SwitchOrg, "POST", "/my-tickets/org", sid, "org=prod-42", nil); resp.StatusCode() != fasthttp.StatusForbidden {
			t.Fatalf("switch without csrf: %d", resp.StatusCode())
		}
		if resp := h.do(h.svc.SwitchOrg, "POST", "/my-tickets/org", sid, "csrf="+sess.CSRF+"&org=prod-99", nil); resp.StatusCode() != fasthttp.StatusNotFound {
			t.Fatalf("switch to a foreign org: %d", resp.StatusCode())
		}
		if resp := h.do(h.svc.SwitchOrg, "POST", "/my-tickets/org", sid, "csrf="+sess.CSRF+"&org=prod-42", nil); resp.StatusCode() != fasthttp.StatusSeeOther {
			t.Fatalf("switch: %d", resp.StatusCode())
		}
		if _, body := page(sid, "list", nil); !strings.Contains(body, "Mobile crash") || strings.Contains(body, "Acme question") || strings.Contains(body, "New ticket") {
			t.Fatal("after switching to BigCorp (read): wrong list or a New ticket button")
		}
		if after := h.svc.store.rdb.TTL(context.Background(), sessionPrefix+sid).Val(); after > ttl || after <= 0 {
			t.Fatalf("switching changed the session lifetime: %v -> %v", ttl, after)
		}
	})

	t.Run("a ticket link from another of the user's orgs switches to it", func(t *testing.T) {
		h.be.tickets["400"] = TicketSummary{UUID: "u400", ReferenceNumber: "400", ContactID: 50, OrgID: "prod-99", Subject: "Not yours", InternalStatus: "Open", CreatedAt: now}
		h.be.msgs["u201"] = []Message{{FromCustomer: true, HTML: "x"}}
		// Opens in Acme (latest ticket); BigCorp access is the Mobile app project only.
		sid, sess := h.signIn([]map[string]any{
			{"id": 7, "name": "Acme", "access": "manage"},
			{"id": 42, "name": "BigCorp", "access": "", "projects": []map[string]any{{"id": 17, "name": "Mobile app", "access": "manage"}}}})
		current := func() string {
			got, _ := h.svc.store.Get(context.Background(), sid)
			return got.OrgID
		}
		if current() != "prod-7" {
			t.Fatalf("opened %q, want prod-7", current())
		}
		notFound := func(ref string) string {
			resp := h.do(h.svc.View, "GET", "/my-tickets/"+ref, sid, "", map[string]string{"ref": ref})
			if resp.StatusCode() != fasthttp.StatusNotFound {
				t.Fatalf("ticket %s: %d, want 404", ref, resp.StatusCode())
			}
			return string(resp.Body())
		}
		// Org-level BigCorp ticket: not visible with project access there, so no switch.
		hidden := notFound("200")
		// Another org's ticket, and a ticket that doesn't exist: the same page, no switch.
		if notFound("400") != hidden || notFound("9999") != hidden || current() != "prod-7" {
			t.Fatal("refused tickets differ from a missing one, or the org switched")
		}
		// A reply from a tab still showing BigCorp's ticket goes through and switches.
		if resp := h.do(h.svc.Reply, "POST", "/my-tickets/201/reply", sid, "csrf="+sess.CSRF+"&message=from+an+old+tab", map[string]string{"ref": "201"}); resp.StatusCode() != fasthttp.StatusSeeOther || len(h.be.msgs["u201"]) != 2 {
			t.Fatalf("reply from another org's tab: %d", resp.StatusCode())
		}
		if current() != "prod-42" {
			t.Fatalf("after the reply the session is in %q, want prod-42", current())
		}
		// And back: a link to the Acme ticket opens it and switches again.
		if resp := h.do(h.svc.View, "GET", "/my-tickets/300", sid, "", map[string]string{"ref": "300"}); resp.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(resp.Body()), "Acme question") {
			t.Fatalf("Acme ticket link: %d", resp.StatusCode())
		}
		if current() != "prod-7" {
			t.Fatalf("after the Acme link the session is in %q", current())
		}
	})

	t.Run("orgs without access never reach the session", func(t *testing.T) {
		c := baseClaims()
		c["orgs"] = []map[string]any{{"id": 5, "name": "None", "access": "", "projects": []map[string]any{{"id": 1, "name": "P", "access": ""}}}}
		if resp := h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "current-secret", c), "", "", nil); resp.StatusCode() == fasthttp.StatusSeeOther {
			t.Fatal("token without any access signed in")
		}
	})
}

func TestDisplaySubject(t *testing.T) {
	for _, c := range []struct{ subject, ref, format, want string }{
		{"Question about new project - #125", "125", "", "Question about new project"},
		{"Question about new project - #125", "125", "#{ref}", "Question about new project"},
		{"Login fails - [ENC-125]", "125", "[ENC-{ref}]", "Login fails"},
		{"Login fails - #125", "126", "", "Login fails - #125"},   // another ticket's number stays
		{"Renamed by an agent", "125", "", "Renamed by an agent"}, // no suffix: unchanged
		{"Price - #125 - #125", "125", "", "Price - #125"},        // only the appended one goes
		{" - #125", "125", "", " - #125"},                         // never leaves an empty subject
	} {
		if got := DisplaySubject(c.subject, c.ref, c.format); got != c.want {
			t.Errorf("DisplaySubject(%q, %q, %q) = %q, want %q", c.subject, c.ref, c.format, got, c.want)
		}
	}
}

func TestPaging(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	// 120 tickets in BigCorp: ticket 1000 is the newest, 1119 the oldest.
	for i := 0; i < 120; i++ {
		ref := fmt.Sprint(1000 + i)
		h.be.tickets[ref] = TicketSummary{UUID: "u" + ref, ReferenceNumber: ref, ContactID: 50, OrgID: "prod-42",
			Subject: "Paged ticket " + ref, InternalStatus: "Open", CreatedAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	list := func(query string) string {
		resp := h.do(h.svc.List, "GET", "/my-tickets"+query, sid, "", nil)
		if resp.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("list %q: %d", query, resp.StatusCode())
		}
		return string(resp.Body())
	}
	has := func(body string, refs ...int) bool {
		for _, r := range refs {
			if !strings.Contains(body, fmt.Sprintf("Paged ticket %d<", r)) {
				return false
			}
		}
		return true
	}

	first := list("")
	if !has(first, 1000, 1049) || has(first, 1050) || !strings.Contains(first, `href="/my-tickets?page=2" rel="next"`) || strings.Contains(first, `rel="prev"`) {
		t.Fatal("page 1: wrong tickets or links")
	}
	if !strings.Contains(first, "Page 1") {
		t.Fatal("page 1: no page label")
	}
	second := list("?page=2")
	if !has(second, 1050, 1099) || has(second, 1049, 1100) || !strings.Contains(second, `href="/my-tickets" rel="prev"`) {
		t.Fatal("page 2: wrong tickets or Newer link")
	}
	// Opening a ticket from page 2 keeps the page, in its link and in the close button.
	if !strings.Contains(second, `href="/my-tickets/1050?page=2"`) {
		t.Fatal("ticket links don't carry the page")
	}
	view := h.do(h.svc.View, "GET", "/my-tickets/1050?page=2", sid, "", map[string]string{"ref": "1050"})
	if body := string(view.Body()); !strings.Contains(body, `href="/my-tickets?page=2" class="btn btn-ghost btn-icon -mr-2`) || !has(body, 1099) {
		t.Fatal("ticket view lost the page")
	}
	last := list("?page=3")
	if !has(last, 1100, 1119) || strings.Contains(last, `rel="next"`) {
		t.Fatal("page 3: wrong tickets, or an Older link at the end")
	}
	if beyond := list("?page=9"); !strings.Contains(beyond, "No older tickets") {
		t.Fatal("past the end: no 'No older tickets'")
	}
	// Junk falls back to page 1; filters stay in the paging links.
	if junk := list("?page=abc"); !has(junk, 1000) {
		t.Fatal("page=abc didn't show page 1")
	}
	if filtered := list("?status=Open"); !strings.Contains(filtered, `href="/my-tickets?page=2&amp;status=Open" rel="next"`) {
		t.Fatal("Older link dropped the status filter")
	}
	// Few tickets: a plain count and no pager.
	sid2, _ := h.signIn([]map[string]any{{"id": 7, "name": "Acme", "access": "manage"}})
	small := string(h.do(h.svc.List, "GET", "/my-tickets", sid2, "", nil).Body())
	if strings.Contains(small, `aria-label="Pages"`) || !strings.Contains(small, "0 tickets") {
		t.Fatal("small org shows a pager")
	}
}

func TestMessagePaging(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.be.tickets["500"] = TicketSummary{UUID: "u500", ReferenceNumber: "500", ContactID: 50, OrgID: "prod-42", Subject: "Long thread", InternalStatus: "Open", CreatedAt: now}
	h.be.tickets["501"] = TicketSummary{UUID: "u501", ReferenceNumber: "501", ContactID: 50, OrgID: "prod-42", Subject: "Short thread", InternalStatus: "Open", CreatedAt: now}
	for i := 1; i <= 25; i++ { // oldest first: message 1 .. 25
		h.be.msgs["u500"] = append(h.be.msgs["u500"], Message{FromCustomer: true, HTML: fmt.Sprintf("<p>Message number %d.</p>", i)})
	}
	h.be.msgs["u501"] = []Message{{FromCustomer: true, HTML: "<p>Message number 1.</p>"}, {HTML: "<p>Message number 2.</p>"}}
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	view := func(ref, query string) string {
		resp := h.do(h.svc.View, "GET", "/my-tickets/"+ref+query, sid, "", map[string]string{"ref": ref})
		if resp.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("ticket %s%s: %d", ref, query, resp.StatusCode())
		}
		return string(resp.Body())
	}
	shows := func(body string, from, to int) bool {
		for i := 1; i <= 25; i++ {
			if strings.Contains(body, fmt.Sprintf("Message number %d.<", i)) != (i >= from && i <= to) {
				return false
			}
		}
		return true
	}

	newest := view("500", "")
	if !shows(newest, 16, 25) || !strings.Contains(newest, "Messages 16–25 of 25") ||
		!strings.Contains(newest, `href="/my-tickets/500?m=2">Show earlier messages`) || strings.Contains(newest, "Show newer messages") {
		t.Fatal("newest page: wrong messages or links")
	}
	middle := view("500", "?m=2")
	if !shows(middle, 6, 15) || !strings.Contains(middle, `href="/my-tickets/500?m=3"`) || !strings.Contains(middle, `href="/my-tickets/500">Show newer messages`) {
		t.Fatal("middle page: wrong messages or links")
	}
	oldest := view("500", "?m=3")
	if !shows(oldest, 1, 5) || !strings.Contains(oldest, "Messages 1–5 of 25") || !strings.Contains(oldest, "Start of the conversation") || strings.Contains(oldest, "Show earlier messages") {
		t.Fatal("oldest page: wrong messages or links")
	}
	if past := view("500", "?m=40"); !shows(past, 1, 5) {
		t.Fatal("a page past the start doesn't show the oldest messages")
	}
	// The list's filters and page stay in the thread links.
	if kept := view("500", "?page=2&status=Open"); !strings.Contains(kept, `href="/my-tickets/500?page=2&amp;status=Open&amp;m=2"`) {
		t.Fatal("thread links dropped the list filters")
	}
	// Short threads: no paging bar.
	if short := view("501", ""); strings.Contains(short, "Show earlier messages") || strings.Contains(short, "Messages 1–") || !strings.Contains(short, "Message number 2.") {
		t.Fatal("short thread shows paging")
	}
	if got := PageMessages(h.be.msgs["u500"], 3, 10); len(got) != 5 || got[0].HTML != "<p>Message number 1.</p>" {
		t.Fatalf("PageMessages oldest page = %d messages", len(got))
	}
}

func TestRenames(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.be.tickets["600"] = TicketSummary{UUID: "u600", ReferenceNumber: "600", ContactID: 50, OrgID: "prod-42",
		ProjectID: "prod-17", ProjectName: "Mobile app", Subject: "Old ticket", InternalStatus: "Open", CreatedAt: now}
	h.be.msgs["u600"] = []Message{{FromCustomer: true, HTML: "x"}}

	// BigCorp and its project were renamed in Encatch since the ticket was raised.
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp Group", "access": "manage",
		"projects": []map[string]any{{"id": 17, "name": "Mobile App (iOS)", "access": "manage"}}}})
	if len(h.be.synced) != 1 || h.be.synced[0].ID != "prod-42" || h.be.synced[0].Name != "BigCorp Group" ||
		len(h.be.synced[0].Projects) != 1 || h.be.synced[0].Projects[0].ID != "prod-17" || h.be.synced[0].Projects[0].Name != "Mobile App (iOS)" {
		t.Fatalf("sign-in didn't pass the current names on for syncing: %+v", h.be.synced)
	}

	// Even with a stale stored name, My Tickets shows the current one.
	tk := h.be.tickets["600"]
	tk.ProjectName = "Mobile app"
	h.be.tickets["600"] = tk
	list := string(h.do(h.svc.List, "GET", "/my-tickets", sid, "", nil).Body())
	view := string(h.do(h.svc.View, "GET", "/my-tickets/600", sid, "", map[string]string{"ref": "600"}).Body())
	for name, body := range map[string]string{"list": list, "ticket": view} {
		if !strings.Contains(body, "Mobile App (iOS)") || strings.Contains(body, ">Mobile app<") {
			t.Errorf("%s shows a stale project name", name)
		}
	}
	if !strings.Contains(view, "BigCorp Group") {
		t.Error("ticket page doesn't show the current org name")
	}
}

func TestTheme(t *testing.T) {
	h := newHarness(t)
	sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	themeCookieOf := func(resp *fasthttp.Response) (string, bool) {
		c := fasthttp.AcquireCookie()
		defer fasthttp.ReleaseCookie(c)
		c.SetKey(themeCookie)
		if !resp.Header.Cookie(c) {
			return "", false
		}
		return string(c.Value()) + "|" + string(c.Path()) + "|" + fmt.Sprint(c.MaxAge() > 0, c.Secure(), c.HTTPOnly()), true
	}
	post := func(form string) *fasthttp.Response {
		return h.do(h.svc.Theme, "POST", "/my-tickets/theme", sid, form, nil)
	}

	resp := post("csrf=" + sess.CSRF + "&theme=dark&back=" + url.QueryEscape("/my-tickets/100?page=2"))
	if got, ok := themeCookieOf(resp); !ok || got != "dark|/my-tickets|true true false" {
		t.Fatalf("dark: cookie %q (set %v)", got, ok)
	}
	if loc := string(resp.Header.Peek("Location")); resp.StatusCode() != fasthttp.StatusSeeOther || loc != "/my-tickets/100?page=2" {
		t.Fatalf("dark: %d to %q", resp.StatusCode(), loc)
	}
	if got, _ := themeCookieOf(post("csrf=" + sess.CSRF + "&theme=system")); !strings.HasPrefix(got, "|/my-tickets|false") {
		t.Fatalf("system should delete the cookie, got %q", got)
	}
	// The toggle's form without theme.js: following the device -> dark -> following again.
	toggle := func(current string) string {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod("POST")
		ctx.Request.SetRequestURI("/my-tickets/theme")
		ctx.Request.Header.SetCookie(cookieName, sid)
		if current != "" {
			ctx.Request.Header.SetCookie(themeCookie, current)
		}
		ctx.Request.Header.SetContentType("application/x-www-form-urlencoded")
		ctx.Request.SetBodyString("csrf=" + sess.CSRF + "&theme=toggle")
		if err := h.svc.Theme(&fastglue.Request{RequestCtx: ctx}); err != nil {
			t.Fatal(err)
		}
		resp := &fasthttp.Response{}
		ctx.Response.CopyTo(resp)
		got, _ := themeCookieOf(resp)
		return strings.SplitN(got, "|", 2)[0]
	}
	if toggle("") != "dark" || toggle("dark") != "" || toggle("light") != "" {
		t.Fatalf("toggle cycle: none->%q dark->%q light->%q", toggle(""), toggle("dark"), toggle("light"))
	}
	if _, ok := themeCookieOf(post("csrf=wrong&theme=dark")); ok {
		t.Fatal("theme saved without a valid csrf token")
	}
	for _, back := range []string{"https://evil.example/x", "//evil.example", "/admin", "/my-ticketsevil", "/my-tickets/\\evil"} {
		resp := post("csrf=" + sess.CSRF + "&theme=light&back=" + url.QueryEscape(back))
		if loc := string(resp.Header.Peek("Location")); loc != "/my-tickets" {
			t.Errorf("back %q redirected to %q", back, loc)
		}
	}

	// Pages render the saved theme, and the switch shows it as selected.
	page := func(theme string) string {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod("GET")
		ctx.Request.SetRequestURI("/my-tickets?status=Open")
		ctx.Request.Header.SetCookie(cookieName, sid)
		if theme != "" {
			ctx.Request.Header.SetCookie(themeCookie, theme)
		}
		if err := h.svc.List(&fastglue.Request{RequestCtx: ctx}); err != nil {
			t.Fatal(err)
		}
		return string(ctx.Response.Body())
	}
	dark := page("dark")
	if !strings.Contains(dark, `<html lang="en" data-theme="dark">`) || !strings.Contains(dark, `name="theme" value="toggle"`) {
		t.Fatal("dark page: no data-theme or no toggle")
	}
	if !strings.Contains(dark, `name="back" value="/my-tickets?status=Open"`) || !strings.Contains(dark, "/my-tickets/assets/theme.js?v=") {
		t.Fatal("page lacks the return path or theme.js")
	}
	system := page("")
	if strings.Contains(system, "data-theme=") {
		t.Fatal("no cookie should follow the device")
	}
	if junk := page("purple"); strings.Contains(junk, "data-theme=") {
		t.Fatal("unknown theme value was rendered")
	}

	// theme.js is served as JavaScript.
	js := h.do(h.svc.Asset, "GET", "/my-tickets/assets/theme.js", "", "", map[string]string{"file": "theme.js"})
	if js.StatusCode() != 200 || !strings.HasPrefix(string(js.Header.ContentType()), "text/javascript") {
		t.Fatalf("theme.js: %d %s", js.StatusCode(), js.Header.ContentType())
	}
}

// The dark tokens appear twice in ui/app.css (chosen Dark, and device dark without a
// Light choice); they must not drift apart.
func TestDarkTokensInSync(t *testing.T) {
	src, err := os.ReadFile("ui/app.css")
	if err != nil {
		t.Fatal(err)
	}
	block := func(selector string) string {
		s := string(src)
		i := strings.Index(s, selector)
		if i < 0 {
			t.Fatalf("%s not found", selector)
		}
		body := s[strings.Index(s[i:], "{")+i+1:]
		body = body[:strings.Index(body, "}")]
		return strings.Join(strings.Fields(body), " ")
	}
	chosen, device := block(`:root[data-theme="dark"]`), block(`:root:not([data-theme="light"])`)
	if chosen != device || !strings.Contains(chosen, "--background:") {
		t.Fatalf("dark token blocks differ:\nchosen: %s\ndevice: %s", chosen, device)
	}
}
