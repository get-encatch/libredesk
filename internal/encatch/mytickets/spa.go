package mytickets

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// The Vue app (web/) is built into web/build/app (pnpm build; the release build does
// it, see .goreleaser.yaml) and embedded here. web/build/README.md is committed so this
// compiles without a Node build; then /my-tickets answers 503 until the app is built.
//
//go:embed all:web/build
var webFS embed.FS

// builtApp is the embedded build, or nil if web/build/app is missing.
var builtApp = func() fs.FS {
	sub, err := fs.Sub(webFS, "web/build/app")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}()

// appCSP allows only this origin's scripts (the build has no inline script). Styles
// may be inline because the UI components position popovers with style attributes.
const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

// App serves the app's page for /my-tickets, /my-tickets/new and /my-tickets/{ref}
// (the router redirects /my-tickets/ here);
// the app routes in the browser. The saved theme goes on <html> here, so the page
// doesn't flash light before the app starts.
func (s *Service) App(r *fastglue.Request) error {
	var page []byte
	if s.app != nil {
		page, _ = fs.ReadFile(s.app, "index.html")
	}
	if page == nil {
		s.lo.Error("my-tickets: the Vue app isn't built into the binary (web/build/app/index.html is missing)")
		r.RequestCtx.SetStatusCode(fasthttp.StatusServiceUnavailable)
		r.RequestCtx.SetContentType("text/plain; charset=utf-8")
		r.RequestCtx.SetBodyString("Encatch Support is unavailable right now. Please try again later.")
		return nil
	}
	if theme := themeOf(r); theme != "" {
		page = bytes.Replace(page, []byte(`<html lang="en">`), []byte(`<html lang="en" data-theme="`+theme+`">`), 1)
	}
	setSecurityHeaders(r.RequestCtx)
	r.RequestCtx.Response.Header.Set("Content-Security-Policy", appCSP)
	r.RequestCtx.SetContentType("text/html; charset=utf-8")
	r.RequestCtx.SetBody(page)
	return nil
}

// AppAsset serves a file of the built app from /my-tickets/assets/{file}. Their
// names carry a content hash, so they can be cached for good.
func (s *Service) AppAsset(r *fastglue.Request) error {
	name, _ := r.RequestCtx.UserValue("file").(string)
	if s.app == nil || name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		r.RequestCtx.SetStatusCode(fasthttp.StatusNotFound)
		return nil
	}
	b, err := fs.ReadFile(s.app, "assets/"+name)
	if err != nil {
		r.RequestCtx.SetStatusCode(fasthttp.StatusNotFound)
		return nil
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h := &r.RequestCtx.Response.Header
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	r.RequestCtx.SetContentType(ctype)
	r.RequestCtx.SetBody(b)
	return nil
}
