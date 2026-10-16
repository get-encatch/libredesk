package mytickets

import (
	"strconv"
	"testing"

	"github.com/zerodha/fastglue"
)

// limited wraps an API handler the way cmd/encatch_mytickets.go does; noSession
// counts the calls that would have gone to the per-address limit.
func limited(s *Service, h func(*Service, *fastglue.Request) error, noSession *int) func(*fastglue.Request) error {
	return func(r *fastglue.Request) error {
		signedIn, allowed := s.LimitAPI(r)
		if !allowed {
			return nil
		}
		if !signedIn {
			*noSession++
		}
		return h(s, r)
	}
}

func TestAPIRateLimitPerSession(t *testing.T) {
	h := newHarness(t)
	h.svc.apiPerMinute = 3
	org := []map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}}
	sidA, _ := h.signIn(org)
	sidB, _ := h.signIn(org) // another customer on the same network

	var noSession int
	session := limited(h.svc, (*Service).APISession, &noSession)
	call := func(sid string) apiResp {
		return h.api(session, apiReq{method: "GET", uri: "/my-tickets/api/session", sid: sid})
	}

	for i := 1; i <= 3; i++ {
		if r := call(sidA); r.code != 200 {
			t.Fatalf("call %d: %d %s", i, r.code, r.raw)
		}
	}
	r := call(sidA)
	if r.code != 429 || r.errCode() != errRateLimited || r.Err.Message != rateLimitedMessage {
		t.Fatalf("over the limit: %d %s", r.code, r.raw)
	}
	if ra, err := strconv.Atoi(string(r.resp.Header.Peek("Retry-After"))); err != nil || ra < 1 || ra > 60 {
		t.Errorf("Retry-After %q", r.resp.Header.Peek("Retry-After"))
	}
	// B has its own budget, although A has used up theirs.
	if r := call(sidB); r.code != 200 {
		t.Errorf("other session limited too: %d %s", r.code, r.raw)
	}

	// Without a session the per-address limit applies (not counted here) and the
	// handler answers 401 as before.
	if r := call(""); r.code != 401 || noSession != 1 {
		t.Errorf("no session: %d (per-address calls %d) %s", r.code, noSession, r.raw)
	}
	if r := call("not-a-session"); r.code != 401 || noSession != 2 {
		t.Errorf("unknown session: %d (per-address calls %d) %s", r.code, noSession, r.raw)
	}
}

func TestAPIRateLimitOff(t *testing.T) {
	h := newHarness(t)
	h.svc.apiPerMinute = 0
	sid, _ := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	var noSession int
	session := limited(h.svc, (*Service).APISession, &noSession)
	for i := 0; i < 50; i++ {
		if r := h.api(session, apiReq{method: "GET", uri: "/my-tickets/api/session", sid: sid}); r.code != 200 {
			t.Fatalf("call %d with no limit: %d", i, r.code)
		}
	}
}

func TestMaskIP(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.77":          "203.0.113.x",
		"2001:db8:1:2:3:4:5:6":  "2001:db8:1:2::",
		"::ffff:198.51.100.200": "198.51.100.x",
		"nonsense":              "unknown",
	} {
		if got := maskIP(in); got != want {
			t.Errorf("maskIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnceAMinute(t *testing.T) {
	var o onceAMinute
	n := 0
	for i := 0; i < 5; i++ {
		o.once("a", func() { n++ })
	}
	o.once("b", func() { n++ })
	if n != 2 {
		t.Errorf("ran %d times, want 2 (once per key)", n)
	}
}
