// Package instancegate (encatch) decides, per Encatch instance, whether a ticket may send
// a kind of message: customer emails, notifications to agents, AI agent emails. Tickets
// raised through My Tickets carry their instance (local, dev, uat, prod) in the
// encatch_instance attribute; tickets without one (email) are never gated. Installed by
// cmd/encatch_mytickets.go into libredesk (internal/conversation/encatch.go,
// internal/notification/encatch.go).
package instancegate

import (
	"slices"

	"github.com/jmoiron/sqlx"
)

// Gate allows a kind of message only for tickets from Instances. A nil Gate, or one that
// isn't Enabled (its setting is absent), allows everything, as upstream. Enabled with no
// Instances allows no Encatch instance at all.
type Gate struct {
	Name      string // for logs, e.g. "customer email"
	Enabled   bool
	Instances []string
	DB        func() *sqlx.DB
	Logf      func(format string, args ...any)
}

// AllowsInstance reports whether a ticket from instance may send this kind of message.
func (g *Gate) AllowsInstance(instance string) bool {
	return g == nil || !g.Enabled || instance == "" || slices.Contains(g.Instances, instance)
}

// Allows reports whether a conversation may send this kind of message. If its instance
// can't be read it allows the message: a missed message on a real ticket is worse than an
// extra one on a test ticket.
func (g *Gate) Allows(conversationID int) bool {
	if g == nil || !g.Enabled {
		return true
	}
	var instance string
	if err := g.DB().Get(&instance, `SELECT COALESCE(custom_attributes->>'encatch_instance', '')
		FROM conversations WHERE id = $1`, conversationID); err != nil {
		g.Logf("instancegate: reading the instance of conversation %d (allowing the %s): %v", conversationID, g.Name, err)
		return true
	}
	if !g.AllowsInstance(instance) {
		g.Logf("instancegate: %s not sent for conversation %d: ticket from Encatch instance %q", g.Name, conversationID, instance)
		return false
	}
	return true
}
