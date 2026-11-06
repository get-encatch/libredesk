package notifier

import "github.com/abhinavxd/libredesk/internal/notification/models"

// encatch: (get-encatch fork) agent notifications for tickets from Encatch test instances.
// cmd/encatch_mytickets.go installs the check (internal/encatch/instancegate); while it is
// nil every notification is sent, as upstream. Used in Dispatcher.Send, which every
// notification to agents goes through (in-app, live, push and email).

var encatchNotifyGate func(conversationID int) bool

// SetEncatchNotifyGate installs the check that decides whether a conversation's
// notifications (in-app, live and email) reach agents.
func SetEncatchNotifyGate(fn func(conversationID int) bool) { encatchNotifyGate = fn }

// encatchMayNotify reports whether n may be sent. Notifications not about a conversation
// are never gated.
func encatchMayNotify(n models.Notification) bool {
	return !n.ConversationID.Valid || encatchNotifyGate == nil || encatchNotifyGate(n.ConversationID.Int)
}
