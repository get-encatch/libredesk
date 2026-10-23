package mytickets

import (
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

func TestAPIResolve(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	for ref, st := range map[string]string{"300": "Open", "301": "Waiting on customer", "302": "Resolved", "303": "Closed", "304": "Auto-closing"} {
		h.be.tickets[ref] = TicketSummary{UUID: "u" + ref, ReferenceNumber: ref, ContactID: 50, OrgID: "prod-42", Subject: "T" + ref, InternalStatus: st, CreatedAt: now}
	}
	h.be.tickets["305"] = TicketSummary{UUID: "u305", ReferenceNumber: "305", ContactID: 50, OrgID: "prod-42", ProjectID: "prod-18", ProjectName: "Website", Subject: "Read-only project", InternalStatus: "Open", CreatedAt: now}
	h.be.tickets["306"] = TicketSummary{UUID: "u306", ReferenceNumber: "306", ContactID: 50, OrgID: "prod-42", Subject: "Org-level", InternalStatus: "Open", CreatedAt: now}

	resolve := func(sid, csrf, ref string, body any) apiResp {
		return h.api(func(r *fastglue.Request) error { return h.svc.APIResolve(r) },
			apiReq{method: "POST", uri: "/my-tickets/api/tickets/" + ref + "/resolve", sid: sid, csrf: csrf, ref: ref, json: body})
	}
	manager, msess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})

	t.Run("manager resolves open and waiting tickets, with or without a note", func(t *testing.T) {
		if r := resolve(manager, msess.CSRF, "300", map[string]string{"note": "  Works now, thanks!  "}); r.code != 200 {
			t.Fatalf("resolve 300: %d %s", r.code, r.raw)
		}
		got := h.be.resolved[len(h.be.resolved)-1]
		if got.uuid != "u300" || got.note != "Works now, thanks!" || got.who != "Anita Rao (anita@bigcorp.com)" {
			t.Fatalf("resolve call = %+v", got)
		}
		for _, ref := range []string{"301", "304"} { // Waiting on you, including Auto-closing
			if r := resolve(manager, msess.CSRF, ref, nil); r.code != 200 {
				t.Fatalf("resolve %s without a body: %d %s", ref, r.code, r.raw)
			}
		}
		if v, _ := h.apiTicket(manager, "300", ""); v.Ticket.Status != StatusResolved {
			t.Fatalf("ticket after resolve: %q", v.Ticket.Status)
		}
	})

	t.Run("already resolved or closed", func(t *testing.T) {
		before := len(h.be.resolved)
		for _, ref := range []string{"300", "302", "303"} {
			if r := resolve(manager, msess.CSRF, ref, nil); r.code != fasthttp.StatusConflict || !strings.Contains(r.Err.Message, "already resolved") {
				t.Fatalf("resolve %s again: %d %s", ref, r.code, r.raw)
			}
		}
		if len(h.be.resolved) != before {
			t.Fatal("an already-resolved ticket was resolved again")
		}
	})

	t.Run("refused requests change nothing", func(t *testing.T) {
		before := len(h.be.resolved)
		if r := resolve(manager, "", "306", nil); r.code != fasthttp.StatusForbidden || r.errCode() != errCSRF {
			t.Fatalf("no CSRF: %d %s", r.code, r.raw)
		}
		if r := resolve(manager, msess.CSRF, "306", map[string]string{"note": strings.Repeat("x", maxResolveNote+1)}); r.code != fasthttp.StatusUnprocessableEntity {
			t.Fatalf("long note: %d %s", r.code, r.raw)
		}
		if r := resolve(manager, msess.CSRF, "999", nil); r.code != fasthttp.StatusNotFound {
			t.Fatalf("unknown ticket: %d", r.code)
		}
		reader, rsess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "read"}})
		if r := resolve(reader, rsess.CSRF, "306", nil); r.code != fasthttp.StatusForbidden || r.errCode() != errReadOnly {
			t.Fatalf("org reader: %d %s", r.code, r.raw)
		}
		project, psess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "", "projects": []map[string]any{
			{"id": 17, "name": "Mobile app", "access": "manage"}, {"id": 18, "name": "Website", "access": "read"}}}})
		if r := resolve(project, psess.CSRF, "305", nil); r.code != fasthttp.StatusForbidden || r.errCode() != errReadOnly {
			t.Fatalf("read-only project: %d %s", r.code, r.raw)
		}
		if r := resolve(project, psess.CSRF, "306", nil); r.code != fasthttp.StatusNotFound {
			t.Fatalf("project user, org-level ticket: %d %s", r.code, r.raw)
		}
		if len(h.be.resolved) != before {
			t.Fatalf("a refused request resolved a ticket: %+v", h.be.resolved[before:])
		}
	})
}

func TestPlainName(t *testing.T) {
	if got := plainName(`Anita <b>"Rao"</b> & co`); got != "Anita bRao/b  co" {
		t.Errorf("plainName = %q", got)
	}
}
