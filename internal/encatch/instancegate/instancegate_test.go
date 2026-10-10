package instancegate

import (
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestAllowsInstance(t *testing.T) {
	prodOnly := &Gate{Enabled: true, Instances: []string{"prod"}}
	notLocal := &Gate{Enabled: true, Instances: []string{"dev", "uat", "prod"}}
	none := &Gate{Enabled: true, Instances: []string{}}
	absent := &Gate{Enabled: false}
	var unset *Gate
	for _, c := range []struct {
		name     string
		g        *Gate
		instance string
		want     bool
	}{
		{"customer email: prod", prodOnly, "prod", true},
		{"customer email: dev", prodOnly, "dev", false},
		{"customer email: uat", prodOnly, "uat", false},
		{"customer email: local", prodOnly, "local", false},
		{"customer email: email ticket", prodOnly, "", true},
		{"notifications: dev", notLocal, "dev", true},
		{"notifications: local", notLocal, "local", false},
		{"AI email: none, even prod", none, "prod", false},
		{"AI email: none, email ticket", none, "", true},
		{"setting absent", absent, "local", true},
		{"no gate installed", unset, "local", true},
		{"no loose matching", prodOnly, "Prod", false},
	} {
		if got := c.g.AllowsInstance(c.instance); got != c.want {
			t.Errorf("%s: AllowsInstance(%q) = %v, want %v", c.name, c.instance, got, c.want)
		}
	}
}

func TestDisabledGateNeverQueries(t *testing.T) {
	g := &Gate{Enabled: false, DB: func() *sqlx.DB { t.Fatal("queried the database"); return nil }, Logf: t.Logf}
	if !g.Allows(1) {
		t.Fatal("a disabled gate blocked a message")
	}
	var unset *Gate
	if !unset.Allows(1) {
		t.Fatal("a nil gate blocked a message")
	}
}
