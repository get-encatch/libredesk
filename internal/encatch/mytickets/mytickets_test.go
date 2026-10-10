package mytickets

import (
	"context"
	"errors"
	"fmt"
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
		Secrets:     map[string][]string{"encatch_dashboard": {"current-secret", "old-secret"}},
		MaxLifetime: 60 * time.Second, Leeway: 30 * time.Second, ValidTiers: tiers,
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
		"iss": "encatch-dashboard", "external_user_id": 9134, "email": "Anita@BigCorp.com", "name": "Anita Rao",
		"org_id": 42, "org_name": "BigCorp", "support_tier": "Growth Plus", "scope": "self",
		"projects": []map[string]any{{"id": 17, "name": "Mobile app"}, {"id": "18", "name": "Website"}},
		"iat":      now.Unix(), "exp": now.Add(60 * time.Second).Unix(), "jti": fmt.Sprintf("j-%d", now.UnixNano()),
	}
}

func TestVerify(t *testing.T) {
	v := testVerifier()
	mod := func(f func(jwt.MapClaims)) jwt.MapClaims { c := baseClaims(); f(c); return c }

	c, err := v.Verify(sign(t, "current-secret", baseClaims()))
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if c.Email != "anita@bigcorp.com" || c.OrgID != "42" || c.ExternalUserID != "9134" || c.Projects[1].ID != "18" {
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
		"no org":            sign(t, "current-secret", mod(func(c jwt.MapClaims) { delete(c, "org_id") })),
		"bad email":         sign(t, "current-secret", mod(func(c jwt.MapClaims) { c["email"] = "nope" })),
		"bad scope":         sign(t, "current-secret", mod(func(c jwt.MapClaims) { c["scope"] = "admin" })),
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

	c, err = v.Verify(sign(t, "current-secret", mod(func(c jwt.MapClaims) { c["support_tier"] = "Platinum"; delete(c, "scope") })))
	if err != nil || c.SupportTier != "" || c.Scope != ScopeSelf {
		t.Fatalf("unknown tier should be dropped and scope default to self: %+v %v", c, err)
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
	projects := []Project{{ID: "17", Name: "Mobile app"}}
	self := Session{ContactID: 1, OrgID: "42", Scope: ScopeSelf, Projects: projects}
	proj := Session{ContactID: 1, OrgID: "42", Scope: ScopeProject, Projects: projects}
	org := Session{ContactID: 1, OrgID: "42", Scope: ScopeOrg}

	mine := TicketSummary{ContactID: 1, OrgID: "42", ProjectID: "17"}
	mineEmail := TicketSummary{ContactID: 1}
	mineOtherOrg := TicketSummary{ContactID: 1, OrgID: "7"}
	teammate := TicketSummary{ContactID: 2, OrgID: "42", ProjectID: "17"}
	teammateOtherProject := TicketSummary{ContactID: 2, OrgID: "42", ProjectID: "99"}
	otherOrgSameProjectID := TicketSummary{ContactID: 3, OrgID: "7", ProjectID: "17"}

	check := func(name string, s Session, tk TicketSummary, want bool) {
		if got := Visible(s, tk); got != want {
			t.Errorf("%s: Visible = %v, want %v", name, got, want)
		}
	}
	check("self sees own", self, mine, true)
	check("self sees own email ticket", self, mineEmail, true)
	check("self never sees own ticket in another org", self, mineOtherOrg, false)
	check("self never sees teammate", self, teammate, false)
	check("project sees teammate in project", proj, teammate, true)
	check("project never sees other project", proj, teammateOtherProject, false)
	check("project never crosses orgs", proj, otherOrgSameProjectID, false)
	check("project skips email tickets", proj, mineEmail, false)
	check("org sees teammate", org, teammateOtherProject, true)
	check("org never crosses orgs", org, otherOrgSameProjectID, false)
}

func TestScopeWhere(t *testing.T) {
	where, args := scopeWhere(ListQuery{Scope: ScopeSelf, ContactID: 5, OrgID: "42", Status: StatusResolved})
	if !strings.Contains(where, "c.contact_id = $1") || !strings.Contains(where, "NOT (c.custom_attributes ? 'org_id')") || len(args) != 2 {
		t.Fatalf("self where = %q %v", where, args)
	}
	where, args = scopeWhere(ListQuery{Scope: ScopeProject, OrgID: "42", ProjectIDs: []string{"17"}})
	if !strings.Contains(where, "'org_id' = $1") || !strings.Contains(where, "ANY($2)") || len(args) != 2 {
		t.Fatalf("project where = %q", where)
	}
	where, _ = scopeWhere(ListQuery{Scope: ScopeOrg, OrgID: "42", ProjectID: "17"})
	if strings.Contains(where, "contact_id") || !strings.Contains(where, "'project_id' = $2") {
		t.Fatalf("org where = %q", where)
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
	msgs     map[string][]Message
}

func newFake() *fakeBackend {
	return &fakeBackend{contacts: map[string]int{}, tickets: map[string]TicketSummary{}, attrs: map[string]map[string]any{}, msgs: map[string][]Message{}}
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
	return out, nil
}
func (f *fakeBackend) GetTicket(ref string) (TicketSummary, error) {
	t, ok := f.tickets[ref]
	if !ok {
		return t, errors.New("not found")
	}
	return t, nil
}
func (f *fakeBackend) Messages(uuid string) ([]Message, error) { return f.msgs[uuid], nil }
func (f *fakeBackend) CreateTicket(contactID int, subject, html string, attrs map[string]any) (string, error) {
	ref := fmt.Sprint(100 + len(f.tickets))
	uuid := "u" + ref
	f.tickets[ref] = TicketSummary{UUID: uuid, ReferenceNumber: ref, Subject: subject, InternalStatus: "Open", ContactID: contactID,
		OrgID: fmt.Sprint(attrs[AttrOrgID]), ProjectID: fmt.Sprint(attrs[AttrProjectID]), RaisedBy: "Anita Rao"}
	f.attrs[ref] = attrs
	f.msgs[uuid] = []Message{{FromCustomer: true, AuthorName: "Anita Rao", HTML: html + `<script>alert(1)</script>`}}
	return ref, nil
}
func (f *fakeBackend) AddReply(contactID int, uuid, html string) error {
	f.msgs[uuid] = append(f.msgs[uuid], Message{FromCustomer: true, HTML: html})
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
		EligibleTiers: []string{"Growth Plus", "Enterprise Standard", "Enterprise Premium"}, SessionTTL: 8 * time.Hour, Logger: &lo})
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

	resp = h.do(h.svc.Create, "POST", "/my-tickets/new", sid, "csrf="+csrf+"&subject=Checkout+broken&message=Steps%0A1.+open&project=17&priority=Urgent", nil)
	if resp.StatusCode() != fasthttp.StatusSeeOther || string(resp.Header.Peek("Location")) != "/my-tickets/100" {
		t.Fatalf("create: %d %q %s", resp.StatusCode(), resp.Header.Peek("Location"), resp.Body())
	}
	a := h.be.attrs["100"]
	if a[AttrOrgID] != "42" || a[AttrTicketTier] != "Growth Plus" || a[AttrRequestedPriority] != "Urgent" ||
		a[AttrProjectID] != "17" || a[AttrProjectName] != "Mobile app" || a[AttrSourceApp] != "encatch-dashboard" {
		t.Fatalf("ticket attrs = %v", a)
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

	// A teammate's ticket is hidden in self scope and returns 404, not 403.
	h.be.tickets["900"] = TicketSummary{UUID: "u900", ReferenceNumber: "900", ContactID: 99, OrgID: "42"}
	if resp := h.do(h.svc.View, "GET", "/my-tickets/900", sid, "", map[string]string{"ref": "900"}); resp.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("teammate ticket in self scope: status %d", resp.StatusCode())
	}

	// SaaS Standard orgs can't request a priority.
	low := baseClaims()
	low["support_tier"] = "SaaS Standard"
	low["jti"] = "low-1"
	sid2 := sessionCookie(t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "current-secret", low), "", "", nil))
	s2, _ := h.svc.store.Get(context.Background(), sid2)
	h.do(h.svc.Create, "POST", "/my-tickets/new", sid2, "csrf="+s2.CSRF+"&subject=Q&message=Q&priority=Urgent", nil)
	if _, ok := h.be.attrs["102"][AttrRequestedPriority]; ok {
		t.Fatalf("SaaS Standard ticket got a requested priority: %v", h.be.attrs["102"])
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
