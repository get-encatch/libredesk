package mytickets

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// formMessage runs messageOf on a urlencoded form.
func formMessage(t *testing.T, form string) message {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.Header.SetContentType("application/x-www-form-urlencoded")
	ctx.Request.SetBodyString(form)
	return messageOf(&fastglue.Request{RequestCtx: ctx})
}

func TestMessageFormatting(t *testing.T) {
	keep := `<p>Steps: <strong>open</strong> the <em>export</em> page</p>` +
		`<ul><li>one</li><li>two</li></ul><ol><li>first</li></ol>` +
		`<p>Run <code>retry=3</code></p><pre><code>line 1
line 2</code></pre><p><b>b</b> <i>i</i><br>next</p>`
	m := formMessage(t, "message_html="+urlq(keep))
	if m.HTML != keep {
		t.Fatalf("allowed formatting changed:\n got %s\nwant %s", m.HTML, keep)
	}
	if !strings.HasPrefix(m.Text, "Steps: open the export page one two first Run retry=3 line 1 line 2") {
		t.Fatalf("text = %q", m.Text)
	}

	// Links: only http(s) and mailto, opened safely.
	m = formMessage(t, "message_html="+urlq(`<p><a href="https://encatch.com/docs">docs</a> <a href="mailto:a@b.co">mail</a> `+
		`<a href="javascript:alert(1)">js</a> <a href="data:text/html,x">data</a> <a href="/relative">rel</a></p>`))
	for _, want := range []string{`<a href="https://encatch.com/docs" rel="nofollow noopener" target="_blank">docs</a>`, `<a href="mailto:a@b.co" rel="nofollow">mail</a>`} {
		if !strings.Contains(m.HTML, want) {
			t.Errorf("missing %s in %s", want, m.HTML)
		}
	}
	for _, bad := range []string{"javascript:", "data:", `href="/relative"`} {
		if strings.Contains(m.HTML, bad) {
			t.Errorf("kept %s: %s", bad, m.HTML)
		}
	}

	// Anything else goes; its text stays where it's harmless.
	hostile := `<p onclick="x()" style="color:red" class="c">Hi</p><script>alert(1)</script><img src=x onerror="alert(1)">` +
		`<iframe src="https://evil.example"></iframe><table><tr><td>cell</td></tr></table><h1>Title</h1>` +
		`<span style="font-size:99px">big</span><svg><script>alert(2)</script></svg><style>p{}</style><form><input></form>` +
		`<blockquote><p>quoted</p></blockquote>`
	m = formMessage(t, "message_html="+urlq(hostile))
	for _, bad := range []string{"onclick", "style", "class", "<script", "alert", "<img", "onerror", "<iframe", "<table", "<td", "<h1", "<span", "<svg", "<form", "<input", "<blockquote"} {
		if strings.Contains(strings.ToLower(m.HTML), bad) {
			t.Errorf("kept %q: %s", bad, m.HTML)
		}
	}
	// A quote's text stays, without the quote (the agent app would fold it away).
	if !strings.Contains(m.HTML, "<p>Hi</p>") || !strings.Contains(m.HTML, "<p>quoted</p>") || !strings.Contains(m.Text, "cell") || !strings.Contains(m.Text, "Title") {
		t.Errorf("lost harmless text: %s / %q", m.HTML, m.Text)
	}
}

func TestMessageEmptyAndLimits(t *testing.T) {
	for _, empty := range []string{"<p></p>", "<p><br></p>", "<p> </p><p></p>", "<p><strong></strong></p>", "<script>x</script>", "<img src=x>"} {
		if m := formMessage(t, "message_html="+urlq(empty)); m.Text != "" || m.HTML != "" || m.TooLarge {
			t.Errorf("%q: %+v, want empty", empty, m)
		}
	}
	// Plain text, as before, when there's no formatted version (or it's blank).
	m := formMessage(t, "message=Line+1%0ALine+2&message_html=++")
	if m.HTML != "<p>Line 1<br>Line 2</p>" || m.Text != "Line 1\nLine 2" {
		t.Fatalf("plain message = %+v", m)
	}
	// The formatted version wins over plain text.
	if m := formMessage(t, "message=plain&message_html="+urlq("<p><em>rich</em></p>")); m.HTML != "<p><em>rich</em></p>" {
		t.Fatalf("formatted not preferred: %+v", m)
	}
	// Text is held to maxMessageLen; markup only to maxMessageHTML.
	long := "<p><strong>" + strings.Repeat("a", maxMessageLen) + "</strong></p>"
	if m := formMessage(t, "message_html="+urlq(long)); len(m.Text) != maxMessageLen || m.TooLarge {
		t.Fatalf("message at the limit: %d chars, too large %v", len(m.Text), m.TooLarge)
	}
	huge := strings.Repeat("<p><em>x</em></p>", maxMessageHTML/16+1)
	if m := formMessage(t, "message_html="+urlq(huge)); !m.TooLarge {
		t.Fatal("oversized markup accepted")
	}
}

// Formatted tickets and replies go through the API to the backend cleaned.
func TestAPIFormattedMessages(t *testing.T) {
	h := newHarness(t)
	sid, sess := h.signIn([]map[string]any{{"id": 42, "name": "BigCorp", "access": "manage"}})
	r := h.apiCreate(sid, sess.CSRF, form("subject", "Rich", "message", "fallback",
		"message_html", `<p>See <strong>this</strong><img src=x onerror=alert(1)></p><ul><li>step</li></ul>`), nil)
	var created struct{ Ref string }
	r.into(t, &created)
	got := h.be.msgs["u"+created.Ref][0].HTML
	if !strings.HasPrefix(got, `<p>See <strong>this</strong></p><ul><li>step</li></ul>`) || strings.Contains(got, "onerror") {
		t.Fatalf("stored HTML = %s", got)
	}
	// A blank editor is an empty message.
	if r := h.apiCreate(sid, sess.CSRF, form("subject", "S", "message_html", "<p></p>"), nil); r.code != fasthttp.StatusUnprocessableEntity ||
		r.Err.Message != "Please describe the problem or question." {
		t.Fatalf("blank editor: %d %s", r.code, r.raw)
	}
	if r := h.apiReply(sid, sess.CSRF, created.Ref, form("message_html", "<p><br></p>"), nil); r.Err == nil || r.Err.Message != "Please write a reply or attach a file." {
		t.Fatalf("blank reply: %d %s", r.code, r.raw)
	}
	if r := h.apiReply(sid, sess.CSRF, created.Ref, form("message_html", `<blockquote><p>q</p></blockquote><p><a href="https://x.io">x</a></p>`), nil); r.code != fasthttp.StatusCreated {
		t.Fatalf("formatted reply: %d %s", r.code, r.raw)
	}
	msgs := h.be.msgs["u"+created.Ref]
	if last := msgs[len(msgs)-1].HTML; last != `<p>q</p><p><a href="https://x.io" rel="nofollow noopener" target="_blank">x</a></p>` {
		t.Fatalf("stored reply = %s", last)
	}
	huge := strings.Repeat("<p><em>x</em></p>", maxMessageHTML/16+1)
	if r := h.apiReply(sid, sess.CSRF, created.Ref, form("message_html", huge), nil); r.code != fasthttp.StatusUnprocessableEntity ||
		!strings.Contains(r.Err.Message, "under 20000 characters") {
		t.Fatalf("oversized reply: %d %s", r.code, r.raw)
	}
}

func urlq(s string) string {
	a := fasthttp.AcquireArgs()
	defer fasthttp.ReleaseArgs(a)
	a.Set("x", s)
	return strings.TrimPrefix(a.String(), "x=")
}
