package conversation

import (
	"testing"

	"github.com/abhinavxd/libredesk/internal/inbox"
)

func TestEncatchEmailGates(t *testing.T) {
	t.Cleanup(func() { SetEncatchEmailGates(nil, nil) })
	if !encatchMayEmail(7, inbox.ChannelEmail) || !encatchMayAIEmail(7) {
		t.Fatal("no gates installed should allow")
	}
	SetEncatchEmailGates(func(id int) bool { return id != 7 }, func(int) bool { return false })
	if encatchMayEmail(7, inbox.ChannelEmail) {
		t.Fatal("gated conversation was emailed")
	}
	if !encatchMayEmail(8, inbox.ChannelEmail) {
		t.Fatal("allowed conversation was not emailed")
	}
	if !encatchMayEmail(7, "livechat") {
		t.Fatal("chat delivery was gated; only email should be")
	}
	if encatchMayAIEmail(8) {
		t.Fatal("AI gate allowing no one let an email through")
	}
}
