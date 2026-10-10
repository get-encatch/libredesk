package redact

import (
	"strings"
	"testing"
)

func TestAllowed(t *testing.T) {
	agent, admin := Actor{ID: 7}, Actor{ID: 1, IsAdmin: true}
	own := Msg{Type: "outgoing", SenderType: "agent", SenderID: 7}
	other := Msg{Type: "outgoing", SenderType: "agent", SenderID: 8}
	customer := Msg{Type: "incoming", SenderType: "contact", SenderID: 7} // same ID as the agent, different table
	activity := Msg{Type: "activity", SenderType: "agent", SenderID: 7}
	for _, c := range []struct {
		a    Actor
		m    Msg
		want error
	}{
		{agent, own, nil},
		{agent, other, ErrForbidden},
		{agent, customer, ErrForbidden},
		{admin, other, nil},
		{admin, customer, nil},
		{agent, activity, ErrNotAllowed},
		{admin, activity, ErrNotAllowed},
	} {
		if got := Allowed(c.a, c.m); got != c.want {
			t.Errorf("Allowed(%+v, %+v) = %v, want %v", c.a, c.m, got, c.want)
		}
	}
}

func TestExposure(t *testing.T) {
	for _, c := range []struct {
		m    Msg
		want string
	}{
		{Msg{Type: "outgoing", Private: true, Status: "sent"}, ""},
		{Msg{Type: "outgoing", Status: "sent"}, "emailed"},
		{Msg{Type: "outgoing", Status: "pending"}, ""},
		{Msg{Type: "outgoing", Status: "failed"}, ""},
		{Msg{Type: "incoming", Status: "received"}, "from_customer"},
	} {
		if got := Exposure(c.m); got != c.want {
			t.Errorf("Exposure(%+v) = %q, want %q", c.m, got, c.want)
		}
	}
}

func TestSafe(t *testing.T) {
	got := safe(`{{ .Contact.Email }}<script>x</script>.env`)
	if strings.Contains(got, "{{") || strings.Contains(got, "<script>") || !strings.Contains(got, "&#123;&#123;") {
		t.Fatalf("safe() = %q", got)
	}
}

func TestWhose(t *testing.T) {
	a := Actor{ID: 7}
	for _, c := range []struct {
		m    Msg
		want string
	}{
		{Msg{Type: "outgoing", SenderType: "agent", SenderID: 7}, "my reply"},
		{Msg{Type: "outgoing", SenderType: "agent", SenderID: 7, Private: true}, "my note"},
		{Msg{Type: "incoming", SenderType: "contact", SenderID: 7}, "the customer's message"},
		{Msg{Type: "outgoing", SenderType: "agent", SenderID: 8}, "an agent's reply"},
		{Msg{Type: "outgoing", SenderType: "agent", SenderID: 8, Private: true}, "an agent's note"},
	} {
		if got := whose(a, c.m); got != c.want {
			t.Errorf("whose(%+v) = %q, want %q", c.m, got, c.want)
		}
	}
}

func TestCheckReason(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"", false},
		{"oops", false},
		{"contained an API key", false}, // 4 words
		{"  contained a live API key  ", true},
		{"customer pasted their database password in the log", true},
		{strings.Repeat("word ", 101), false}, // over 500 characters
	} {
		got, err := CheckReason(c.in)
		if (err == nil) != c.ok {
			t.Errorf("CheckReason(%q) error = %v, want ok=%v", c.in, err, c.ok)
		}
		if err == nil && got != strings.TrimSpace(c.in) {
			t.Errorf("CheckReason(%q) = %q, want trimmed", c.in, got)
		}
	}
}
