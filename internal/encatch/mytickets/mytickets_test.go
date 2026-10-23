package mytickets

import (
	"context"
	"errors"
	"fmt"
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
		"Waiting on customer": StatusWaitingYou, "Auto-closing": StatusWaitingYou, "Resolved": StatusResolved, "Closed": StatusResolved,
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
	resolved []resolveCall     // from ResolveByCustomer
}

type resolveCall struct{ uuid, who, note string }

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

func (f *fakeBackend) ResolveByCustomer(uuid, who, note string) error {
	f.resolved = append(f.resolved, resolveCall{uuid, who, note})
	for ref, t := range f.tickets {
		if t.UUID == uuid {
			t.InternalStatus = "Resolved"
			f.tickets[ref] = t
		}
	}
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

// signIn signs a token with the given orgs for user 9134 and returns the session id.
func (h *harness) signIn(orgs []map[string]any) (string, Session) {
	c := baseClaims()
	c["orgs"] = orgs
	c["jti"] = fmt.Sprintf("j-%d", time.Now().UnixNano())
	sid := sessionCookie(h.t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(h.t, "current-secret", c), "", "", nil))
	sess, _ := h.svc.store.Get(context.Background(), sid)
	return sid, sess
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
