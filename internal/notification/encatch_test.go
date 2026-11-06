package notifier

import (
	"testing"

	"github.com/abhinavxd/libredesk/internal/notification/channels"
	"github.com/abhinavxd/libredesk/internal/notification/models"
	"github.com/volatiletech/null/v9"
)

func TestEncatchMayNotify(t *testing.T) {
	t.Cleanup(func() { SetEncatchNotifyGate(nil) })
	if !encatchMayNotify(models.Notification{ConversationID: null.IntFrom(7)}) {
		t.Fatal("no gate installed should allow")
	}
	SetEncatchNotifyGate(func(id int) bool { return id != 7 })
	if encatchMayNotify(models.Notification{ConversationID: null.IntFrom(7)}) {
		t.Fatal("gated conversation was notified")
	}
	if !encatchMayNotify(models.Notification{ConversationID: null.IntFrom(8)}) {
		t.Fatal("allowed conversation was not notified")
	}
	if !encatchMayNotify(models.Notification{}) {
		t.Fatal("a notification without a conversation was gated")
	}
}

// The gate sits in Dispatcher.Send, so a gated ticket reaches no channel at all.
func TestEncatchDispatcherGate(t *testing.T) {
	t.Cleanup(func() { SetEncatchNotifyGate(nil) })
	inApp := &fakeChannelProvider{channel: models.NotificationChannelInApp, result: channels.Result{Sent: true}}
	email := &fakeChannelProvider{channel: models.NotificationChannelEmail, result: channels.Result{Sent: true}}
	d := NewDispatcher(DispatcherOpts{
		Pipeline: channels.NewPipeline(email, inApp),
		Prefs: fakePreferences{channels: map[int][]models.NotificationChannel{
			42: {models.NotificationChannelInApp, models.NotificationChannelEmail},
		}},
	})
	SetEncatchNotifyGate(func(id int) bool { return id != 7 })
	send := func(conversationID int) {
		t.Helper()
		if _, err := d.Send(models.Notification{
			Type:           models.NotificationTypeMention,
			Recipients:     []models.Recipient{{UserID: 42}},
			ConversationID: null.IntFrom(conversationID),
		}); err != nil {
			t.Fatal(err)
		}
	}

	send(7)
	if len(inApp.deliveries) != 0 || len(email.deliveries) != 0 {
		t.Fatalf("gated ticket delivered: in-app %d, email %d", len(inApp.deliveries), len(email.deliveries))
	}
	send(8)
	if len(inApp.deliveries) != 1 || len(email.deliveries) != 1 {
		t.Fatalf("allowed ticket: in-app %d, email %d, want 1 each", len(inApp.deliveries), len(email.deliveries))
	}
}
