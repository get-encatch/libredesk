package mytickets

import (
	"html"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/zerodha/fastglue"
)

// Customers can format messages in the app's editor: bold, italic, bulleted and
// numbered lists, links, inline code and code blocks. Not quotes: the agent app folds
// <blockquote> away as quoted email history, which would hide a customer's text from
// agents; a pasted quote keeps its text without the quote. The editor sends HTML in
// "message_html"; formatPolicy keeps only those elements, so nothing else a request
// carries (images, tables, styles, scripts, event handlers) is stored. Without
// "message_html" the plain "message" field is used, as before.
var formatPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "b", "em", "i", "ul", "ol", "li", "pre", "code")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}()

// maxMessageHTML caps the formatted HTML of one message, markup included. The text
// is held to maxMessageLen; this only stops oversized markup.
const maxMessageHTML = 5 * maxMessageLen

// message is a customer's message as submitted: the HTML to store and its text,
// which the emptiness and length checks use.
type message struct {
	HTML, Text string
	TooLarge   bool // the formatted HTML is over maxMessageHTML
}

var (
	textOnly   = bluemonday.StrictPolicy()
	blockEnd   = regexp.MustCompile(`(?i)</(p|li|pre)>|<br\s*/?>`)
	whitespace = regexp.MustCompile(`\s+`)
	emptyParas = regexp.MustCompile(`(?i)^(\s*<p>\s*(<br\s*/?>)?\s*</p>\s*)*$`)
)

// messageOf reads the message of a new ticket or reply from the form.
func messageOf(r *fastglue.Request) message {
	if raw := string(r.RequestCtx.FormValue("message_html")); strings.TrimSpace(raw) != "" {
		if len(raw) > maxMessageHTML {
			return message{TooLarge: true}
		}
		clean := strings.TrimSpace(formatPolicy.Sanitize(raw))
		text := htmlText(clean)
		if text == "" || emptyParas.MatchString(clean) {
			return message{}
		}
		return message{HTML: clean, Text: text}
	}
	text := strings.TrimSpace(string(r.RequestCtx.FormValue("message")))
	if text == "" {
		return message{}
	}
	return message{HTML: textToHTML(text), Text: text}
}

// htmlText returns the text of message HTML, with blocks and line breaks as spaces.
func htmlText(h string) string {
	t := html.UnescapeString(textOnly.Sanitize(blockEnd.ReplaceAllString(h, "$0 ")))
	return strings.TrimSpace(whitespace.ReplaceAllString(t, " "))
}
