package conversation

// encatch: (get-encatch fork) emails for tickets from Encatch test instances.
// cmd/encatch_mytickets.go installs the checks (internal/encatch/instancegate); while they
// are nil every email is sent, as upstream.

import "github.com/abhinavxd/libredesk/internal/inbox"

var (
	encatchEmailGate   func(conversationID int) bool // customer emails (sendOutgoingMessage)
	encatchAIEmailGate func(conversationID int) bool // AI agent emails (SendTransientEmail)
)

// SetEncatchEmailGates installs the checks for customer emails and AI agent emails.
func SetEncatchEmailGates(customer, ai func(conversationID int) bool) {
	encatchEmailGate, encatchAIEmailGate = customer, ai
}

// encatchMayEmail reports whether a message may go out through channel for this
// conversation. Only email is gated; chat delivery is unaffected.
func encatchMayEmail(conversationID int, channel string) bool {
	return channel != inbox.ChannelEmail || encatchEmailGate == nil || encatchEmailGate(conversationID)
}

// encatchMayAIEmail reports whether the AI agent may email this conversation's customer.
func encatchMayAIEmail(conversationID int) bool {
	return encatchAIEmailGate == nil || encatchAIEmailGate(conversationID)
}
