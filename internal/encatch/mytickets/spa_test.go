package mytickets

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

const testIndex = `<!doctype html>
<html lang="en">
<head><script type="module" crossorigin src="/my-tickets/assets/index-abc123.js"></script></head>
<body><div id="app"></div></body>
</html>`

func testApp() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                {Data: []byte(testIndex)},
		"assets/index-abc123.js":    {Data: []byte("console.log(1)")},
		"assets/index-def456.css":   {Data: []byte("body{}")},
		"assets/logo-light-x9.svg":  {Data: []byte("<svg/>")},
		"assets/logo-dark-y8.png":   {Data: []byte("\x89PNG")},
		"assets/font-z7.woff2":      {Data: []byte("wOF2")},
		"assets/.hidden":            {Data: []byte("x")},
		"assets/nested/inner-1.js":  {Data: []byte("x")},
		"assets/odd-file.unknownxt": {Data: []byte("x")},
	}
}

func location(resp *fasthttp.Response) string { return string(resp.Header.Peek("Location")) }

func TestLogin(t *testing.T) {
	h := newHarness(t)
	tok := sign(t, "current-secret", baseClaims())

	// A good token: a session cookie for /my-tickets and a clean redirect to the app.
	resp := h.do(h.svc.Login, "GET", "/my-tickets/login?token="+tok, "", "", nil)
	if resp.StatusCode() != fasthttp.StatusSeeOther || location(resp) != "/my-tickets" {
		t.Fatalf("login: %d to %q", resp.StatusCode(), location(resp))
	}
	if string(resp.Header.Peek("Referrer-Policy")) != "no-referrer" {
		t.Fatal("the login redirect must send Referrer-Policy: no-referrer (the token is in the URL)")
	}
	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey(cookieName)
	if !resp.Header.Cookie(c) || len(c.Value()) < 20 || string(c.Path()) != basePath || !c.HTTPOnly() || !c.Secure() ||
		c.SameSite() != fasthttp.CookieSameSiteLaxMode {
		t.Fatalf("session cookie: %s", c.String())
	}

	// POST, as core-accounts' /support page signs in.
	posted := baseClaims()
	posted["jti"] = "posted-1"
	if resp := h.do(h.svc.Login, "POST", "/my-tickets/login", "", "token="+sign(t, "current-secret", posted), nil); location(resp) != "/my-tickets" {
		t.Fatalf("POST login went to %q", location(resp))
	}

	// Failures send the browser to the app with a reason it explains, and no session.
	noAccess := baseClaims()
	noAccess["jti"] = "no-access-1"
	noAccess["orgs"] = []map[string]any{{"id": 5, "name": "None", "access": ""}}
	expired := baseClaims()
	expired["jti"] = "expired-1"
	expired["iat"], expired["exp"] = time.Now().Add(-11*time.Minute).Unix(), time.Now().Add(-10*time.Minute).Unix()
	for name, c := range map[string]struct{ method, uri, form, want string }{
		"replayed":     {"GET", "/my-tickets/login?token=" + tok, "", "used"},
		"missing":      {"GET", "/my-tickets/login", "", "missing"},
		"garbage":      {"GET", "/my-tickets/login?token=abc.def.ghi", "", "invalid"},
		"wrong secret": {"GET", "/my-tickets/login?token=" + sign(t, "other-secret", baseClaims()), "", "invalid"},
		"no access":    {"POST", "/my-tickets/login", "token=" + sign(t, "current-secret", noAccess), "invalid"},
		"expired":      {"GET", "/my-tickets/login?token=" + sign(t, "current-secret", expired), "", "invalid"},
	} {
		resp := h.do(h.svc.Login, c.method, c.uri, "", c.form, nil)
		if resp.StatusCode() != fasthttp.StatusSeeOther || location(resp) != "/my-tickets?signin="+c.want {
			t.Errorf("%s: %d to %q, want ?signin=%s", name, resp.StatusCode(), location(resp), c.want)
		}
		c := fasthttp.AcquireCookie()
		c.SetKey(cookieName)
		if resp.Header.Cookie(c) {
			t.Errorf("%s: set a session cookie", name)
		}
		fasthttp.ReleaseCookie(c)
	}
}

// app requests the app's page with an optional theme cookie.
func (h *harness) app(theme string) *fasthttp.Response {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/my-tickets/131?status=Open")
	if theme != "" {
		ctx.Request.Header.SetCookie(themeCookie, theme)
	}
	if err := h.svc.App(&fastglue.Request{RequestCtx: ctx}); err != nil {
		h.t.Fatal(err)
	}
	resp := &fasthttp.Response{}
	ctx.Response.CopyTo(resp)
	return resp
}

func TestApp(t *testing.T) {
	h := newHarness(t)
	h.svc.app = testApp()

	resp := h.app("")
	body := string(resp.Body())
	if resp.StatusCode() != 200 || body != testIndex || !strings.HasPrefix(string(resp.Header.ContentType()), "text/html") {
		t.Fatalf("page: %d %q", resp.StatusCode(), body)
	}
	for k, want := range map[string]string{
		"Cache-Control": "no-store", "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff",
		"Referrer-Policy": "no-referrer", "Content-Security-Policy": appCSP,
	} {
		if got := string(resp.Header.Peek(k)); got != want {
			t.Errorf("%s = %q", k, got)
		}
	}
	csp := string(resp.Header.Peek("Content-Security-Policy"))
	if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "unsafe-eval") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP allows too much: %s", csp)
	}

	// The saved theme is rendered on <html>, so the page doesn't flash; anything else is ignored.
	if body := string(h.app("dark").Body()); !strings.Contains(body, `<html lang="en" data-theme="dark">`) {
		t.Fatal("dark theme not rendered")
	}
	if body := string(h.app("light").Body()); !strings.Contains(body, `<html lang="en" data-theme="light">`) {
		t.Fatal("light theme not rendered")
	}
	if body := string(h.app(`dark"><script>`).Body()); body != testIndex {
		t.Fatal("an unknown theme value was rendered")
	}

	// A binary without the build says so instead of serving a blank page.
	h.svc.app = nil
	if resp := h.app(""); resp.StatusCode() != fasthttp.StatusServiceUnavailable {
		t.Fatalf("without the build: %d", resp.StatusCode())
	}
}

func TestAppAsset(t *testing.T) {
	h := newHarness(t)
	h.svc.app = testApp()
	get := func(name string) *fasthttp.Response {
		return h.do(h.svc.AppAsset, "GET", "/my-tickets/assets/"+name, "", "", map[string]string{"file": name})
	}
	for name, ctype := range map[string]string{
		"index-abc123.js":    "text/javascript",
		"index-def456.css":   "text/css",
		"logo-light-x9.svg":  "image/svg+xml",
		"logo-dark-y8.png":   "image/png",
		"font-z7.woff2":      "font/woff2",
		"odd-file.unknownxt": "application/octet-stream",
	} {
		resp := get(name)
		if resp.StatusCode() != 200 || !strings.HasPrefix(string(resp.Header.ContentType()), ctype) || len(resp.Body()) == 0 ||
			string(resp.Header.Peek("Cache-Control")) != "public, max-age=31536000, immutable" ||
			string(resp.Header.Peek("X-Content-Type-Options")) != "nosniff" {
			t.Errorf("%s: %d %q %q", name, resp.StatusCode(), resp.Header.ContentType(), resp.Header.Peek("Cache-Control"))
		}
	}
	// Only plain files directly in assets/.
	for _, name := range []string{"", "missing.js", ".hidden", "nested/inner-1.js", "../index.html", `..\index.html`, "index.html"} {
		if resp := get(name); resp.StatusCode() != fasthttp.StatusNotFound {
			t.Errorf("%q: %d, want 404", name, resp.StatusCode())
		}
	}
	h.svc.app = nil
	if resp := get("index-abc123.js"); resp.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("without the build: %d", resp.StatusCode())
	}
}

// When the app has been built (pnpm build in web/), the embedded copy is a real page
// whose scripts and styles are all among its assets.
func TestEmbeddedBuild(t *testing.T) {
	if builtApp == nil {
		t.Skip("the Vue app isn't built (web/build/app); the release build checks it")
	}
	h := newHarness(t)
	h.svc.app = builtApp
	page := string(h.app("").Body())
	if !strings.Contains(page, `<div id="app">`) || strings.Contains(page, "<script>") {
		t.Fatalf("built index.html looks wrong (an inline script would break the CSP): %.300s", page)
	}
	refs := 0
	for _, part := range strings.Split(page, `"/my-tickets/assets/`)[1:] {
		name := part[:strings.IndexByte(part, '"')]
		if resp := h.do(h.svc.AppAsset, "GET", "/my-tickets/assets/"+name, "", "", map[string]string{"file": name}); resp.StatusCode() != 200 {
			t.Errorf("index.html refers to %s, which isn't served: %d", name, resp.StatusCode())
		}
		refs++
	}
	if refs < 2 {
		t.Fatalf("index.html refers to %d assets, want the script and the stylesheet", refs)
	}
}
