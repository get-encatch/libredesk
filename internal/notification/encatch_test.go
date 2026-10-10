package notifier

import (
	"testing"

	"github.com/volatiletech/null/v9"
)

func TestEncatchMayNotify(t *testing.T) {
	t.Cleanup(func() { SetEncatchNotifyGate(nil) })
	if !encatchMayNotify(Notification{ConversationID: null.IntFrom(7)}) {
		t.Fatal("no gate installed should allow")
	}
	SetEncatchNotifyGate(func(id int) bool { return id != 7 })
	if encatchMayNotify(Notification{ConversationID: null.IntFrom(7)}) {
		t.Fatal("gated conversation was notified")
	}
	if !encatchMayNotify(Notification{ConversationID: null.IntFrom(8)}) {
		t.Fatal("allowed conversation was not notified")
	}
	if !encatchMayNotify(Notification{}) {
		t.Fatal("a notification without a conversation was gated")
	}
}
