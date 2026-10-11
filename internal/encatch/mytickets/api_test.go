package mytickets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// apiReq is one JSON API request in a test.
type apiReq struct {
	method, uri string
	sid, csrf   string            // session cookie and X-CSRF-Token header ("" = none)
	form        url.Values        // urlencoded body
	json        any               // JSON body
	files       []testFile        // with form fields, sent as multipart
	ref         string            // the {ref} route value
	extra       map[string]string // other route values
}

// apiResp is a decoded API response.
type apiResp struct {
	code int
	raw  string
	resp *fasthttp.Response
	Data json.RawMessage `json:"data"`
	Err  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a apiResp) errCode() string {
	if a.Err == nil {
		return ""
	}
	return a.Err.Code
}

// into decodes the response's data.
func (a apiResp) into(t *testing.T, v any) {
	t.Helper()
	if a.Err != nil {
		t.Fatalf("expected data, got error %d %s: %s", a.code, a.Err.Code, a.Err.Message)
	}
	if err := json.Unmarshal(a.Data, v); err != nil {
		t.Fatalf("decoding %s: %v", a.raw, err)
	}
}

func (h *harness) api(handler func(*fastglue.Request) error, q apiReq) apiResp {
	h.t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(q.method)
	ctx.Request.SetRequestURI(q.uri)
	if q.sid != "" {
		ctx.Request.Header.SetCookie(cookieName, q.sid)
	}
	if q.csrf != "" {
		ctx.Request.Header.Set(csrfHeader, q.csrf)
	}
	switch {
	case q.files != nil:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, vs := range q.form {
			for _, v := range vs {
				_ = w.WriteField(k, v)
			}
		}
		for _, f := range q.files {
			part, _ := w.CreateFormFile(f.field, f.name)
			_, _ = part.Write(f.data)
		}
		_ = w.Close()
		ctx.Request.Header.SetContentType(w.FormDataContentType())
		ctx.Request.SetBody(buf.Bytes())
	case q.form != nil:
		ctx.Request.Header.SetContentType("application/x-www-form-urlencoded")
		ctx.Request.SetBodyString(q.form.Encode())
	case q.json != nil:
		b, _ := json.Marshal(q.json)
		ctx.Request.Header.SetContentType("application/json")
		ctx.Request.SetBody(b)
	}
	if q.ref != "" {
		ctx.SetUserValue("ref", q.ref)
	}
	for k, v := range q.extra {
		ctx.SetUserValue(k, v)
	}
	if err := handler(&fastglue.Request{RequestCtx: ctx}); err != nil {
		h.t.Fatalf("handler error: %v", err)
	}
	resp := &fasthttp.Response{}
	ctx.Response.CopyTo(resp)
	out := apiResp{code: resp.StatusCode(), raw: string(resp.Body()), resp: resp}
	if ct := string(resp.Header.ContentType()); ct != "application/json; charset=utf-8" {
		h.t.Fatalf("%s %s: content type %q", q.method, q.uri, ct)
	}
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		h.t.Fatalf("%s %s: not JSON: %s", q.method, q.uri, resp.Body())
	}
	return out
}

// Typed helpers for the common calls.

func (h *harness) apiSession(sid string) (apiSession, apiResp) {
	r := h.api(h.svc.APISession, apiReq{method: "GET", uri: "/my-tickets/api/session", sid: sid})
	var v apiSession
	if r.code == 200 {
		r.into(h.t, &v)
	}
	return v, r
}

func (h *harness) apiList(sid, query string) apiList {
	h.t.Helper()
	r := h.api(h.svc.APIList, apiReq{method: "GET", uri: "/my-tickets/api/tickets" + query, sid: sid})
	if r.code != 200 {
		h.t.Fatalf("list %q: %d %s", query, r.code, r.raw)
	}
	var v apiList
	r.into(h.t, &v)
	return v
}

func (h *harness) apiTicket(sid, ref, query string) (apiTicketView, apiResp) {
	r := h.api(h.svc.APITicket, apiReq{method: "GET", uri: "/my-tickets/api/tickets/" + ref + query, sid: sid, ref: ref})
	var v apiTicketView
	if r.code == 200 {
		r.into(h.t, &v)
	}
	return v, r
}

func (h *harness) apiCreate(sid, csrf string, form url.Values, files []testFile) apiResp {
	return h.api(h.svc.APICreate, apiReq{method: "POST", uri: "/my-tickets/api/tickets", sid: sid, csrf: csrf, form: form, files: files})
}

func (h *harness) apiReply(sid, csrf, ref string, form url.Values, files []testFile) apiResp {
	return h.api(h.svc.APIReply, apiReq{method: "POST", uri: "/my-tickets/api/tickets/" + ref + "/replies", sid: sid, csrf: csrf, form: form, files: files, ref: ref})
}

func (h *harness) apiNewTicket(sid string) (apiNewTicket, apiResp) {
	r := h.api(h.svc.APINewTicket, apiReq{method: "GET", uri: "/my-tickets/api/new-ticket", sid: sid})
	var v apiNewTicket
	if r.code == 200 {
		r.into(h.t, &v)
	}
	return v, r
}

func refs(l apiList) []string {
	out := make([]string, 0, len(l.Tickets))
	for _, t := range l.Tickets {
		out = append(out, t.Ref)
	}
	return out
}

func hasRef(l apiList, ref string) bool {
	for _, t := range l.Tickets {
		if t.Ref == ref {
			return true
		}
	}
	return false
}

func form(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func TestAPIFlow(t *testing.T) {
	h := newHarness(t)
	sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage",
		"projects": []map[string]any{{"id": 17, "name": "Mobile app", "access": "manage"}, {"id": 18, "name": "Website", "access": "read"}}}})
	csrf := sess.CSRF

	// Without a session every endpoint says the session ended, as JSON.
	for name, call := range map[string]func() apiResp{
		"session": func() apiResp { _, r := h.apiSession(""); return r },
		"list":    func() apiResp { return h.api(h.svc.APIList, apiReq{method: "GET", uri: "/my-tickets/api/tickets"}) },
		"ticket":  func() apiResp { _, r := h.apiTicket("", "100", ""); return r },
		"new":     func() apiResp { _, r := h.apiNewTicket("nonsense"); return r },
		"create":  func() apiResp { return h.apiCreate("", csrf, form("subject", "S", "message", "M"), nil) },
		"reply":   func() apiResp { return h.apiReply("", csrf, "100", form("message", "M"), nil) },
		"org": func() apiResp {
			return h.api(h.svc.APISwitchOrg, apiReq{method: "POST", csrf: csrf, json: map[string]string{"org": "prod-42"}})
		},
	} {
		if r := call(); r.code != fasthttp.StatusUnauthorized || r.errCode() != errSessionEnded || !strings.Contains(r.Err.Message, "8 hours") {
			t.Errorf("%s without a session: %d %s", name, r.code, r.raw)
		}
	}

	// The session describes the user, the org and what they may do.
	s, r := h.apiSession(sid)
	if r.code != 200 || s.User.Name != "Anita Rao" || s.User.Initials != "AR" || s.Org.ID != "prod-42" || s.Org.Name != "BigCorp" ||
		s.Org.Access != "manage" || s.Org.AccessLabel != "All tickets" || !s.CanCreate || s.CSRF != csrf || len(s.Orgs) != 1 ||
		len(s.Projects) != 2 || s.Projects[1].Access != "manage" || s.User.Email != "anita@bigcorp.com" || s.SessionTTL != "8 hours" ||
		s.Uploads == nil || s.Uploads.Accept != ".pdf,.png,.txt" || s.Uploads.MaxFiles != 5 || s.Limits.Subject != maxSubjectLen {
		t.Fatalf("session = %d %s", r.code, r.raw)
	}
	if d := time.Until(s.ExpiresAt); d < 7*time.Hour || d > 8*time.Hour {
		t.Fatalf("expires in %v", d)
	}
	for k, want := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer"} {
		if got := string(r.resp.Header.Peek(k)); got != want {
			t.Errorf("header %s = %q", k, got)
		}
	}

	// Changes need the CSRF header: missing or wrong is refused.
	for _, c := range []string{"", "wrong"} {
		if r := h.apiCreate(sid, c, form("subject", "Hi", "message", "Body"), nil); r.code != fasthttp.StatusForbidden || r.errCode() != errCSRF {
			t.Fatalf("create with csrf %q: %d %s", c, r.code, r.raw)
		}
	}
	// The token in a form field isn't enough: the API only reads the header.
	if r := h.apiCreate(sid, "", form("csrf", csrf, "subject", "Hi", "message", "Body"), nil); r.code != fasthttp.StatusForbidden {
		t.Fatalf("csrf in the body accepted: %d", r.code)
	}

	// A project the user can't see, or a missing field, is a problem to show; nothing is created.
	for name, f := range map[string]url.Values{
		"foreign project": form("subject", "Hi", "message", "Body", "project", "999"),
		"no subject":      form("subject", " ", "message", "Body"),
		"no message":      form("subject", "Hi"),
		"long subject":    form("subject", strings.Repeat("a", maxSubjectLen+1), "message", "Body"),
	} {
		if r := h.apiCreate(sid, csrf, f, nil); r.code != fasthttp.StatusUnprocessableEntity || r.errCode() != errInvalid || r.Err.Message == "" {
			t.Fatalf("%s: %d %s", name, r.code, r.raw)
		}
	}
	if len(h.be.tickets) != 0 {
		t.Fatal("an invalid ticket was created")
	}

	r = h.apiCreate(sid, csrf, form("subject", "Checkout broken", "message", "Steps\n1. open", "project", "prod-17", "priority", "Urgent"), nil)
	var created struct{ Ref string }
	r.into(t, &created)
	if r.code != fasthttp.StatusCreated || created.Ref != "100" {
		t.Fatalf("create: %d %s", r.code, r.raw)
	}
	a := h.be.attrs["100"]
	if a[AttrOrgID] != "prod-42" || a[AttrTicketTier] != "Growth Plus" || a[AttrRequestedPriority] != "Urgent" ||
		a[AttrProjectID] != "prod-17" || a[AttrProjectName] != "Mobile app" || a[AttrSourceApp] != "encatch-accounts-prod" || a[AttrInstance] != "prod" {
		t.Fatalf("ticket attrs = %v", a)
	}
	if tags := h.be.tags["100"]; len(tags) != 1 || tags[0] != "instance:prod" {
		t.Fatalf("ticket tags = %v", tags)
	}

	if l := h.apiList(sid, ""); len(l.Tickets) != 1 || l.Tickets[0].Subject != "Checkout broken" || l.Tickets[0].Status != StatusOpen ||
		l.Tickets[0].ProjectName != "Mobile app" || !l.ShowProject || l.Page != 1 || l.HasOlder || l.HasNewer {
		t.Fatalf("list = %+v", l)
	}

	// The thread's HTML is sanitised.
	v, r := h.apiTicket(sid, "100", "")
	if r.code != 200 || len(v.Messages) != 1 || !strings.Contains(v.Messages[0].HTML, "Steps") || strings.Contains(r.raw, "<script") ||
		!v.CanReply || v.OrgID != "prod-42" || v.Ticket.Status != StatusOpen || !v.Ticket.RaisedByYou || v.Messages[0].Initials != "AR" {
		t.Fatalf("ticket: %d %s", r.code, r.raw)
	}

	if r := h.apiReply(sid, "", "100", form("message", "More info"), nil); r.code != fasthttp.StatusForbidden {
		t.Fatalf("reply without csrf: %d", r.code)
	}
	if r := h.apiReply(sid, csrf, "100", form("message", "More info"), nil); r.code != fasthttp.StatusCreated || len(h.be.msgs["u100"]) != 2 {
		t.Fatalf("reply: %d %s", r.code, r.raw)
	}
	if r := h.apiReply(sid, csrf, "100", form("message", "  "), nil); r.code != fasthttp.StatusUnprocessableEntity || r.Err.Message != "Please write a reply or attach a file." {
		t.Fatalf("empty reply: %d %s", r.code, r.raw)
	}

	// Another org's ticket and a missing one get the same 404.
	h.be.tickets["900"] = TicketSummary{UUID: "u900", ReferenceNumber: "900", ContactID: 1, OrgID: "prod-7", ProjectID: "prod-17"}
	_, other := h.apiTicket(sid, "900", "")
	_, missing := h.apiTicket(sid, "9999", "")
	if other.code != fasthttp.StatusNotFound || other.raw != missing.raw || other.errCode() != errNotFound {
		t.Fatalf("other org's ticket: %d %s / %s", other.code, other.raw, missing.raw)
	}
	if r := h.apiReply(sid, csrf, "900", form("message", "hi"), nil); r.code != fasthttp.StatusNotFound || len(h.be.msgs["u900"]) != 0 {
		t.Fatalf("reply to another org's ticket: %d", r.code)
	}

	// The helpdesk owns the tier: an admin moved the org to SaaS Standard after sign-in,
	// so priorities are no longer offered and a requested one is ignored.
	h.be.tiers["prod-42"] = "SaaS Standard"
	if n, r := h.apiNewTicket(sid); r.code != 200 || n.CanChoosePriority || !n.AllowOrgLevel || len(n.Projects) != 2 || n.Projects[0].ID != "prod-17" ||
		n.DefaultProject != "" || n.Email != "anita@bigcorp.com" || n.OrgName != "BigCorp" {
		t.Fatalf("new ticket after tier change: %d %s", r.code, r.raw)
	}
	r = h.apiCreate(sid, csrf, form("subject", "Q", "message", "Q", "priority", "Urgent"), nil)
	r.into(t, &created)
	if a := h.be.attrs[created.Ref]; a[AttrTicketTier] != "SaaS Standard" || a[AttrRequestedPriority] != nil || a[AttrProjectID] != nil {
		t.Fatalf("ticket after tier change: %v", a)
	}

	// Logout needs the CSRF header, then ends the session.
	logout := func(c string) apiResp {
		return h.api(h.svc.APILogout, apiReq{method: "POST", uri: "/my-tickets/api/logout", sid: sid, csrf: c})
	}
	if r := logout(""); r.code != fasthttp.StatusForbidden {
		t.Fatalf("logout without csrf: %d", r.code)
	}
	if _, err := h.svc.store.Get(context.Background(), sid); err != nil {
		t.Fatal("logout without csrf ended the session")
	}
	if r := logout(csrf); r.code != 200 {
		t.Fatalf("logout: %d %s", r.code, r.raw)
	}
	if _, err := h.svc.store.Get(context.Background(), sid); !errors.Is(err, ErrNoSession) {
		t.Fatal("session still valid after logout")
	}
	if _, r := h.apiSession(sid); r.code != fasthttp.StatusUnauthorized {
		t.Fatalf("session after logout: %d", r.code)
	}
}

func TestAPIAccess(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.be.tickets["200"] = TicketSummary{UUID: "u200", ReferenceNumber: "200", ContactID: 50, OrgID: "prod-42", Subject: "Org-level billing", InternalStatus: "Open", CreatedAt: now}
	h.be.tickets["201"] = TicketSummary{UUID: "u201", ReferenceNumber: "201", ContactID: 50, OrgID: "prod-42", ProjectID: "prod-17", ProjectName: "Mobile app", Subject: "Mobile crash", InternalStatus: "Open", CreatedAt: now}
	h.be.tickets["202"] = TicketSummary{UUID: "u202", ReferenceNumber: "202", ContactID: 50, OrgID: "prod-42", ProjectID: "prod-18", ProjectName: "Website", Subject: "Website slow", InternalStatus: "Open", CreatedAt: now}
	h.be.msgs["u201"] = []Message{{FromCustomer: true, HTML: "x"}}
	h.be.msgs["u202"] = []Message{{FromCustomer: true, HTML: "x"}}

	t.Run("org reader: sees everything, raises and replies nothing", func(t *testing.T) {
		sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "read"}})
		if l := h.apiList(sid, ""); !hasRef(l, "200") || !hasRef(l, "201") || !hasRef(l, "202") {
			t.Fatalf("reader list = %v", refs(l))
		}
		if s, _ := h.apiSession(sid); s.CanCreate || s.Org.AccessLabel != "All tickets · read-only" {
			t.Fatalf("reader session: can_create %v, label %q", s.CanCreate, s.Org.AccessLabel)
		}
		if _, r := h.apiNewTicket(sid); r.code != fasthttp.StatusForbidden || r.errCode() != errReadOnly {
			t.Fatalf("reader new-ticket: %d", r.code)
		}
		if r := h.apiCreate(sid, sess.CSRF, form("subject", "S", "message", "M"), nil); r.code != fasthttp.StatusForbidden || r.errCode() != errReadOnly {
			t.Fatalf("reader create: %d", r.code)
		}
		if v, r := h.apiTicket(sid, "201", ""); r.code != 200 || v.CanReply {
			t.Fatalf("reader ticket: %d can_reply %v", r.code, v.CanReply)
		}
		if r := h.apiReply(sid, sess.CSRF, "201", form("message", "hi"), nil); r.code != fasthttp.StatusForbidden || r.errCode() != errReadOnly || len(h.be.msgs["u201"]) != 1 {
			t.Fatalf("reader reply: %d, msgs %d", r.code, len(h.be.msgs["u201"]))
		}
	})

	t.Run("project manager: own projects only, no org-level", func(t *testing.T) {
		sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "", "projects": []map[string]any{
			{"id": 17, "name": "Mobile app", "access": "manage"}, {"id": 18, "name": "Website", "access": "read"}}}})
		if l := h.apiList(sid, ""); hasRef(l, "200") || !hasRef(l, "201") || !hasRef(l, "202") {
			t.Fatalf("project list = %v", refs(l))
		}
		if s, _ := h.apiSession(sid); !s.CanCreate || s.Org.Access != "" || s.Org.AccessLabel != "Your projects" {
			t.Fatalf("project session: %+v", s.Org)
		}
		if _, r := h.apiTicket(sid, "200", ""); r.code != fasthttp.StatusNotFound {
			t.Fatalf("project user opened an org-level ticket: %d", r.code)
		}
		n, _ := h.apiNewTicket(sid)
		if n.AllowOrgLevel || len(n.Projects) != 1 || n.Projects[0].Name != "Mobile app" || n.DefaultProject != "prod-17" {
			t.Fatalf("new-ticket offers org-level or a read-only project: %+v", n)
		}
		before := len(h.be.tickets)
		for _, project := range []string{"", "prod-18", "prod-99"} {
			if r := h.apiCreate(sid, sess.CSRF, form("subject", "S", "message", "M", "project", project), nil); r.code != fasthttp.StatusUnprocessableEntity ||
				!strings.Contains(r.Err.Message, "choose one of your projects") {
				t.Fatalf("create in %q: %d %s", project, r.code, r.raw)
			}
		}
		if r := h.apiCreate(sid, sess.CSRF, form("subject", "S", "message", "M", "project", "prod-17"), nil); r.code != fasthttp.StatusCreated || len(h.be.tickets) != before+1 {
			t.Fatalf("create in managed project: %d", r.code)
		}
		if v, _ := h.apiTicket(sid, "202", ""); v.CanReply {
			t.Fatal("read-only project ticket offers a reply")
		}
		if r := h.apiReply(sid, sess.CSRF, "202", form("message", "hi"), nil); r.code != fasthttp.StatusForbidden || len(h.be.msgs["u202"]) != 1 {
			t.Fatalf("reply on a read-only project: %d", r.code)
		}
		if r := h.apiReply(sid, sess.CSRF, "201", form("message", "hi"), nil); r.code != fasthttp.StatusCreated {
			t.Fatalf("reply on a managed project: %d", r.code)
		}
	})

	t.Run("two orgs: opens the latest ticket's org and switches", func(t *testing.T) {
		h.be.contacts["prod-9134"] = 77
		h.be.tickets["300"] = TicketSummary{UUID: "u300", ReferenceNumber: "300", ContactID: 77, OrgID: "prod-7", Subject: "Acme question", InternalStatus: "Open", CreatedAt: now.Add(time.Hour)}
		sid, sess := h.signIn([]map[string]any{
			{"id": 42, "name": "BigCorp", "access": "read"},
			{"id": 7, "name": "Acme", "access": "manage"}})
		s, _ := h.apiSession(sid)
		if s.Org.ID != "prod-7" || len(s.Orgs) != 2 {
			t.Fatalf("opened %q with %d orgs, want prod-7 of 2", s.Org.ID, len(s.Orgs))
		}
		if l := h.apiList(sid, ""); !hasRef(l, "300") || hasRef(l, "201") {
			t.Fatalf("Acme list = %v", refs(l))
		}
		ttl := h.svc.store.rdb.TTL(context.Background(), sessionPrefix+sid).Val()
		sw := func(csrf string, body any) apiResp {
			return h.api(h.svc.APISwitchOrg, apiReq{method: "POST", uri: "/my-tickets/api/org", sid: sid, csrf: csrf, json: body})
		}
		if r := sw("", map[string]string{"org": "prod-42"}); r.code != fasthttp.StatusForbidden {
			t.Fatalf("switch without csrf: %d", r.code)
		}
		for _, body := range []any{map[string]string{"org": "prod-99"}, map[string]int{"org": 42}, "junk"} {
			if r := sw(sess.CSRF, body); r.code != fasthttp.StatusNotFound {
				t.Fatalf("switch to %v: %d", body, r.code)
			}
		}
		r := sw(sess.CSRF, map[string]string{"org": "prod-42"})
		var after apiSession
		r.into(t, &after)
		if r.code != 200 || after.Org.ID != "prod-42" || after.CanCreate {
			t.Fatalf("switch: %d %s", r.code, r.raw)
		}
		if l := h.apiList(sid, ""); !hasRef(l, "201") || hasRef(l, "300") {
			t.Fatalf("after switching to BigCorp: %v", refs(l))
		}
		if got := h.svc.store.rdb.TTL(context.Background(), sessionPrefix+sid).Val(); got > ttl || got <= 0 {
			t.Fatalf("switching changed the session lifetime: %v -> %v", ttl, got)
		}
	})

	t.Run("a ticket from another of the user's orgs switches to it", func(t *testing.T) {
		h.be.tickets["400"] = TicketSummary{UUID: "u400", ReferenceNumber: "400", ContactID: 50, OrgID: "prod-99", Subject: "Not yours", InternalStatus: "Open", CreatedAt: now}
		h.be.msgs["u201"] = []Message{{FromCustomer: true, HTML: "x"}}
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
		// Org-level BigCorp ticket (project access only there), another org's ticket and a
		// missing one: the same 404, and no switch.
		var bodies []string
		for _, ref := range []string{"200", "400", "9999"} {
			_, r := h.apiTicket(sid, ref, "")
			if r.code != fasthttp.StatusNotFound {
				t.Fatalf("ticket %s: %d", ref, r.code)
			}
			bodies = append(bodies, r.raw)
		}
		if bodies[0] != bodies[1] || bodies[1] != bodies[2] || current() != "prod-7" {
			t.Fatal("refused tickets differ from a missing one, or the org switched")
		}
		// A reply from a tab still showing BigCorp's ticket goes through and switches.
		if r := h.apiReply(sid, sess.CSRF, "201", form("message", "from an old tab"), nil); r.code != fasthttp.StatusCreated || len(h.be.msgs["u201"]) != 2 {
			t.Fatalf("reply from another org's tab: %d", r.code)
		}
		if current() != "prod-42" {
			t.Fatalf("after the reply the session is in %q", current())
		}
		// Opening the Acme ticket switches back, and says so.
		if v, r := h.apiTicket(sid, "300", ""); r.code != 200 || v.OrgID != "prod-7" || v.Ticket.Subject != "Acme question" {
			t.Fatalf("Acme ticket: %d %s", r.code, r.raw)
		}
		if current() != "prod-7" {
			t.Fatalf("after the Acme ticket the session is in %q", current())
		}
	})
}

// Org 42 on uat and org 42 on prod are different customers.
func TestAPIInstanceIsolation(t *testing.T) {
	h := newHarness(t)
	h.svc.verifier.Secrets["encatch_accounts_uat"] = []string{"uat-secret"}
	c := baseClaims()
	c["iss"], c["instance"] = "encatch-accounts-uat", "uat"
	sid := sessionCookie(t, h.do(h.svc.Login, "GET", "/my-tickets/login?token="+sign(t, "uat-secret", c), "", "", nil))
	h.be.tickets["700"] = TicketSummary{UUID: "u700", ReferenceNumber: "700", ContactID: 50, OrgID: "prod-42", Subject: "Prod org ticket", InternalStatus: "Open"}
	h.be.tickets["701"] = TicketSummary{UUID: "u701", ReferenceNumber: "701", ContactID: 51, OrgID: "uat-42", Subject: "Uat org ticket", InternalStatus: "Open"}
	if l := h.apiList(sid, ""); hasRef(l, "700") || !hasRef(l, "701") {
		t.Fatalf("uat list = %v", refs(l))
	}
	if _, r := h.apiTicket(sid, "700", ""); r.code != fasthttp.StatusNotFound {
		t.Fatalf("uat user opened a prod ticket: %d", r.code)
	}
}

func TestAPIUploads(t *testing.T) {
	h := newHarness(t)
	sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	fields := form("subject", "Crash on export", "message", "See attached")
	pdf := testFile{"files", "report.pdf", []byte("%PDF-1.4 test")}

	if n, _ := h.apiNewTicket(sid); n.Uploads == nil || n.Uploads.Accept != ".pdf,.png,.txt" || n.Uploads.MaxMB != 1 ||
		strings.Join(n.Uploads.Extensions, ",") != "pdf,png,txt" || n.Uploads.MaxTotalMB != 0 {
		t.Fatalf("new-ticket uploads = %+v", n.Uploads)
	}

	for name, files := range map[string][]testFile{
		"type":  {{"files", "setup.exe", []byte("MZ")}},
		"size":  {{"files", "big.pdf", bytes.Repeat([]byte("a"), 1<<20+1)}},
		"count": {pdf, pdf, pdf, pdf, pdf, pdf},
		"empty": {{"files", "empty.txt", nil}},
	} {
		if r := h.apiCreate(sid, sess.CSRF, fields, files); r.code != fasthttp.StatusUnprocessableEntity || r.errCode() != errInvalid || len(h.be.tickets) != 0 {
			t.Fatalf("%s: %d %s, tickets %d", name, r.code, r.raw, len(h.be.tickets))
		}
	}

	// Valid files reach the backend; an empty part is ignored and a path in the name dropped.
	r := h.apiCreate(sid, sess.CSRF, fields, []testFile{pdf, {"files", "../../etc/notes.txt", []byte("hello")}, {"files", "", nil}})
	var created struct{ Ref string }
	r.into(t, &created)
	if r.code != fasthttp.StatusCreated || len(h.be.files) != 2 || h.be.files[0].Name != "report.pdf" ||
		string(h.be.files[0].Data) != "%PDF-1.4 test" || h.be.files[1].Name != "notes.txt" {
		t.Fatalf("create with files: %d, files %+v", r.code, h.be.files)
	}

	// A reply may be just a file; a bad file is refused and nothing is added.
	if r := h.apiReply(sid, sess.CSRF, created.Ref, form("message", ""), []testFile{{"files", "shot.png", []byte("png")}}); r.code != fasthttp.StatusCreated || len(h.be.files) != 3 {
		t.Fatalf("file-only reply: %d, files %d", r.code, len(h.be.files))
	}
	if r := h.apiReply(sid, sess.CSRF, created.Ref, form("message", "Keep"), []testFile{{"files", "x.exe", []byte("MZ")}}); r.code != fasthttp.StatusUnprocessableEntity ||
		!strings.Contains(r.Err.Message, "file type isn") || len(h.be.files) != 3 {
		t.Fatalf("bad file reply: %d %s", r.code, r.raw)
	}
	// Multipart posts still need the CSRF header.
	if r := h.apiReply(sid, "", created.Ref, form("message", "x"), []testFile{pdf}); r.code != fasthttp.StatusForbidden || len(h.be.files) != 3 {
		t.Fatalf("multipart without csrf: %d", r.code)
	}

	// Total limit across files.
	h.svc.uploadLimits = func() UploadLimits { return UploadLimits{MaxMB: 1, MaxTotalMB: 1, Extensions: []string{"txt"}} }
	if s, _ := h.apiSession(sid); s.Uploads.MaxTotalMB != 1 || s.Uploads.Accept != ".txt" {
		t.Fatalf("session uploads = %+v", s.Uploads)
	}
	half := bytes.Repeat([]byte("a"), 600<<10)
	if r := h.apiCreate(sid, sess.CSRF, fields, []testFile{{"files", "a.txt", half}, {"files", "b.txt", half}}); r.code != fasthttp.StatusUnprocessableEntity ||
		!strings.Contains(r.Err.Message, "up to 1 MB in total") {
		t.Fatalf("over the total: %d %s", r.code, r.raw)
	}
	// No attachments at all: uploads is null.
	h.svc.uploadLimits = func() UploadLimits { return UploadLimits{MaxMB: 1} }
	if _, r := h.apiSession(sid); !strings.Contains(r.raw, `"uploads":null`) {
		t.Fatalf("no extensions should mean no uploads: %s", r.raw)
	}
	// Any type: no accept filter.
	h.svc.uploadLimits = func() UploadLimits { return UploadLimits{MaxMB: 1, Extensions: []string{"*"}} }
	if s, _ := h.apiSession(sid); s.Uploads == nil || s.Uploads.Accept != "" {
		t.Fatalf("any type: %+v", s.Uploads)
	}
}

func TestAPIPaging(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	for i := 0; i < 120; i++ { // 1000 is the newest, 1119 the oldest
		ref := fmt.Sprint(1000 + i)
		h.be.tickets[ref] = TicketSummary{UUID: "u" + ref, ReferenceNumber: ref, ContactID: 50, OrgID: "prod-42",
			Subject: "Paged ticket " + ref, InternalStatus: "Open", CreatedAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	check := func(query string, page, first, last int, newer, older bool) {
		t.Helper()
		l := h.apiList(sid, query)
		if l.Page != page || l.HasNewer != newer || l.HasOlder != older || l.PerPage != defaultPerPage {
			t.Fatalf("%q: page %d newer %v older %v", query, l.Page, l.HasNewer, l.HasOlder)
		}
		if first < 0 {
			if len(l.Tickets) != 0 {
				t.Fatalf("%q: %d tickets, want none", query, len(l.Tickets))
			}
			return
		}
		if len(l.Tickets) != last-first+1 || l.Tickets[0].Ref != fmt.Sprint(first) || l.Tickets[len(l.Tickets)-1].Ref != fmt.Sprint(last) {
			t.Fatalf("%q: %d tickets from %s", query, len(l.Tickets), l.Tickets[0].Ref)
		}
	}
	check("", 1, 1000, 1049, false, true)
	check("?page=2", 2, 1050, 1099, true, true)
	check("?page=3", 3, 1100, 1119, true, false)
	check("?page=9", 9, -1, -1, true, false)
	check("?page=abc", 1, 1000, 1049, false, true)
	check(fmt.Sprintf("?page=%d", maxPage+5), maxPage, -1, -1, true, false)

	// Filters come back cleaned: an unknown status and an unseen project are dropped,
	// and search is trimmed and capped.
	l := h.apiList(sid, "?status=Open&project=prod-17&q="+url.QueryEscape("  "+strings.Repeat("é", maxSearchLen+10)+" "))
	if l.Status != StatusOpen || l.Project != "" || len([]rune(l.Search)) != maxSearchLen || l.ShowProject {
		t.Fatalf("filters = %q %q %d runes", l.Status, l.Project, len([]rune(l.Search)))
	}
	if l := h.apiList(sid, "?status=Waiting+on+engineering"); l.Status != "" {
		t.Fatalf("internal status accepted as a filter: %q", l.Status)
	}
}

func TestAPIMessagePaging(t *testing.T) {
	h := newHarness(t)
	h.be.tickets["500"] = TicketSummary{UUID: "u500", ReferenceNumber: "500", ContactID: 50, OrgID: "prod-42", Subject: "Long thread", InternalStatus: "Waiting on customer"}
	h.be.tickets["501"] = TicketSummary{UUID: "u501", ReferenceNumber: "501", ContactID: 50, OrgID: "prod-42", Subject: "Short thread", InternalStatus: "Closed"}
	for i := 1; i <= 25; i++ {
		h.be.msgs["u500"] = append(h.be.msgs["u500"], Message{FromCustomer: true, AuthorName: "Ravi Kumar", HTML: fmt.Sprintf("<p>Message number %d.</p>", i)})
	}
	h.be.msgs["u501"] = []Message{{FromCustomer: true, HTML: "<p>one</p>"}, {AuthorName: "Rahul Gorad", HTML: "<p>two</p>",
		Attachments: []Attachment{{Name: "fix.pdf", URL: "/uploads/x?signature=y"}}}}
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	check := func(query string, page, from, to int, earlier, newer bool) {
		t.Helper()
		v, r := h.apiTicket(sid, "500", query)
		th := v.Thread
		if r.code != 200 || th.Page != page || th.From != from || th.To != to || th.Total != 25 || th.HasEarlier != earlier || th.HasNewer != newer ||
			len(v.Messages) != to-from+1 || v.Messages[0].HTML != fmt.Sprintf("<p>Message number %d.</p>", from) {
			t.Fatalf("%q: %d %+v, %d messages", query, r.code, th, len(v.Messages))
		}
	}
	check("", 1, 16, 25, true, false)
	check("?m=2", 2, 6, 15, true, true)
	check("?m=3", 3, 1, 5, false, true)
	check("?m=40", 3, 1, 5, false, true)

	v, _ := h.apiTicket(sid, "500", "")
	if v.Ticket.Status != StatusWaitingYou || v.Messages[0].Author != "Ravi Kumar" || v.Messages[0].Initials != "RK" || v.Ticket.RaisedByYou {
		t.Fatalf("long thread: %+v", v.Ticket)
	}
	v, _ = h.apiTicket(sid, "501", "")
	m := v.Messages
	if v.Ticket.Status != StatusResolved || v.Thread.HasEarlier || v.Thread.Total != 2 || m[0].Author != "Customer" || m[1].FromCustomer ||
		m[1].Author != "Rahul Gorad" || m[1].Initials != "ES" || len(m[1].Attachments) != 1 || m[1].Attachments[0].URL != "/uploads/x?signature=y" ||
		m[0].Attachments == nil {
		t.Fatalf("short thread: %+v", m)
	}
}

func TestAPIRenames(t *testing.T) {
	h := newHarness(t)
	h.be.tickets["600"] = TicketSummary{UUID: "u600", ReferenceNumber: "600", ContactID: 50, OrgID: "prod-42",
		ProjectID: "prod-17", ProjectName: "Mobile app", Subject: "Old ticket", InternalStatus: "Open"}
	h.be.msgs["u600"] = []Message{{FromCustomer: true, HTML: "x"}}
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp Group", "access": "manage",
		"projects": []map[string]any{{"id": 17, "name": "Mobile App (iOS)", "access": "manage"}}}})
	// A stale stored name still shows the current one.
	tk := h.be.tickets["600"]
	tk.ProjectName = "Mobile app"
	h.be.tickets["600"] = tk
	if l := h.apiList(sid, ""); l.Tickets[0].ProjectName != "Mobile App (iOS)" {
		t.Fatalf("list project name %q", l.Tickets[0].ProjectName)
	}
	if v, _ := h.apiTicket(sid, "600", ""); v.Ticket.ProjectName != "Mobile App (iOS)" {
		t.Fatalf("ticket project name %q", v.Ticket.ProjectName)
	}
	if s, _ := h.apiSession(sid); s.Org.Name != "BigCorp Group" {
		t.Fatalf("org name %q", s.Org.Name)
	}
}

// Every API route is registered once and points at a handler that answers JSON.
func TestAPIRoutes(t *testing.T) {
	h := newHarness(t)
	seen := map[string]bool{}
	for _, rt := range APIRoutes {
		key := rt.Method + " " + rt.Path
		if seen[key] || !strings.HasPrefix(rt.Path, basePath+"/api/") || (rt.Method != "GET" && rt.Method != "POST") {
			t.Errorf("bad route %s", key)
		}
		seen[key] = true
		r := h.api(func(r *fastglue.Request) error { return rt.Handler(h.svc, r) }, apiReq{method: rt.Method, uri: rt.Path, ref: "1"})
		if r.code != fasthttp.StatusUnauthorized {
			t.Errorf("%s without a session: %d", key, r.code)
		}
	}
	// The app reads these from the session; a zero limit would block every message.
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	if s, _ := h.apiSession(sid); s.Limits.Message == 0 || s.Limits.Search == 0 {
		t.Fatalf("limits = %+v", s.Limits)
	}
}
