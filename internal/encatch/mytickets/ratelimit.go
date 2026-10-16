package mytickets

// Rate limits for the JSON API. A signed-in customer's calls are counted per session
// (Opts.APIRequestsPerMinute), so customers who share an address (an office network)
// don't share one budget, and a client stuck re-fetching a page there can't block
// them (2026-10-16). Calls without a session fall back to libredesk's per-address
// "public" limit (cmd/encatch_mytickets.go). Every limit that trips is logged, at most
// once a minute per session or address: libredesk's own limiter logs nothing.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

const (
	errRateLimited = "rate_limited" // 429: wait and try again

	// DefaultAPIRequestsPerMinute is the per-session budget when the config sets none.
	DefaultAPIRequestsPerMinute = 120

	rateLimitedMessage = "Too many requests right now. Please wait a minute and try again; what you've written is kept."
	sessionCtxKey      = "my_tickets_session"
)

// CountRequest adds a request to the session's current one-minute window and returns
// the window's count. The key holds a hash of the session token, never the token.
func (s *Store) CountRequest(ctx context.Context, token string, now time.Time) (int64, error) {
	sum := sha256.Sum256([]byte(token))
	key := fmt.Sprintf("my_tickets:rl:%x:%d", sum[:12], now.Unix()/60)
	pipe := s.rdb.TxPipeline()
	n := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 2*time.Minute)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return n.Val(), nil
}

// LimitAPI applies the per-session limit to an API call. signedIn is false when the
// call has no valid session: the caller then applies the per-address limit, and the
// handler answers 401. When allowed is false the 429 has been written.
func (s *Service) LimitAPI(r *fastglue.Request) (signedIn, allowed bool) {
	sess, ok := s.session(r)
	if !ok {
		return false, true
	}
	// The handler reads the session from here instead of Redis again.
	r.RequestCtx.SetUserValue(sessionCtxKey, sess)
	if s.apiPerMinute <= 0 {
		return true, true
	}
	ctx, cancel := redisCtx()
	defer cancel()
	now := time.Now()
	n, err := s.store.CountRequest(ctx, string(r.RequestCtx.Request.Header.Cookie(cookieName)), now)
	if err != nil {
		// Like libredesk's limiter: a Redis problem mustn't stop customers.
		s.lo.Error("my-tickets: counting request for rate limit", "error", err)
		return true, true
	}
	if n <= int64(s.apiPerMinute) {
		return true, true
	}
	s.limitLog.once(fmt.Sprintf("session:%d:%s", sess.ContactID, sess.Instance), func() {
		s.lo.Warn("my-tickets: rate limited", "by", "session", "instance", sess.Instance, "org", sess.OrgID,
			"contact_id", sess.ContactID, "limit_per_minute", s.apiPerMinute, "path", string(r.RequestCtx.Path()))
	})
	s.RateLimited(r, 60-int(now.Unix()%60))
	return true, false
}

// RateLimited writes the API's 429. retryAfter is in seconds.
func (s *Service) RateLimited(r *fastglue.Request, retryAfter int) {
	if retryAfter < 1 {
		retryAfter = 60
	}
	r.RequestCtx.Response.Header.Set("Retry-After", strconv.Itoa(retryAfter))
	_ = s.apiFail(r, fasthttp.StatusTooManyRequests, errRateLimited, rateLimitedMessage)
}

// AddressLimited logs that libredesk's per-address limit refused a My Tickets request
// (rule "public" for the API without a session, "auth" for sign-in).
func (s *Service) AddressLimited(r *fastglue.Request, rule string) {
	addr := maskIP(r.RequestCtx.RemoteIP().String())
	if fwd := string(r.RequestCtx.Request.Header.Peek("X-Forwarded-For")); fwd != "" {
		addr = maskIP(strings.TrimSpace(strings.Split(fwd, ",")[0]))
	}
	s.limitLog.once("address:"+rule+":"+addr, func() {
		s.lo.Warn("my-tickets: rate limited", "by", "address", "rule", rule, "address", addr,
			"path", string(r.RequestCtx.Path()), "user_agent", string(r.RequestCtx.UserAgent()))
	})
}

// maskIP keeps the network part of an address for the log: 203.0.113.x, 2001:db8:1:2::.
func maskIP(s string) string {
	ip := net.ParseIP(s)
	switch {
	case ip == nil:
		return "unknown"
	case ip.To4() != nil:
		v4 := ip.To4()
		return fmt.Sprintf("%d.%d.%d.x", v4[0], v4[1], v4[2])
	default:
		return ip.Mask(net.CIDRMask(64, 128)).String()
	}
}

// onceAMinute runs a function at most once a minute per key (for log lines).
type onceAMinute struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (o *onceAMinute) once(key string, fn func()) {
	now := time.Now()
	o.mu.Lock()
	if o.last == nil {
		o.last = map[string]time.Time{}
	}
	if t, ok := o.last[key]; ok && now.Sub(t) < time.Minute {
		o.mu.Unlock()
		return
	}
	o.last[key] = now
	if len(o.last) > 10000 { // forget old keys now and then
		for k, t := range o.last {
			if now.Sub(t) >= time.Minute {
				delete(o.last, k)
			}
		}
	}
	o.mu.Unlock()
	fn()
}
