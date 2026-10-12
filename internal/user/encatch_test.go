package user

// encatch: tests for ChangeOwnPassword (encatch.go).

import (
	"testing"

	"github.com/abhinavxd/libredesk/internal/envelope"
	"golang.org/x/crypto/bcrypt"
)

func TestEncatchChangeOwnPassword(t *testing.T) {
	u, db := newTestManager(t)
	const current, next = "Current-pass-1", "Brand-new-pass-2"

	agent, err := u.CreateAgent("Pass", "Word", "pass.word@example.test", []string{"Agent"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(current), bcrypt.MinCost)
	db.MustExec(`UPDATE users SET password = $1 WHERE id = $2`, hash, agent.ID)

	stored := func() string {
		var h string
		if err := db.Get(&h, `SELECT password FROM users WHERE id = $1`, agent.ID); err != nil {
			t.Fatalf("reading password: %v", err)
		}
		return h
	}
	before := stored()

	rejected := []struct{ name, current, next string }{
		{"wrong current password", "Not-the-pass-1", next},
		{"empty current password", "", next},
		{"same as current", current, current},
		{"weak new password", current, "short"},
		{"no special character", current, "Abcdefghij1"},
	}
	for _, c := range rejected {
		if _, err := u.ChangeOwnPassword(agent.ID, c.current, c.next); err == nil {
			t.Errorf("%s: accepted", c.name)
		} else if e, ok := err.(envelope.Error); !ok || e.ErrorType != envelope.InputError {
			t.Errorf("%s: want an input error, got %v", c.name, err)
		}
		if stored() != before {
			t.Fatalf("%s: password changed", c.name)
		}
	}

	got, err := u.ChangeOwnPassword(agent.ID, current, next)
	if err != nil {
		t.Fatalf("valid change: %v", err)
	}
	if got.Email.String != "pass.word@example.test" {
		t.Errorf("returned agent email %q", got.Email.String)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored()), []byte(next)) != nil {
		t.Fatal("new password doesn't verify")
	}
	if _, err := u.VerifyPassword("pass.word@example.test", []byte(current)); err == nil {
		t.Error("old password still signs in")
	}
	if _, err := u.VerifyPassword("pass.word@example.test", []byte(next)); err != nil {
		t.Errorf("new password doesn't sign in: %v", err)
	}
}
