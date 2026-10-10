// Package orgtiers keeps each Encatch customer org's support tier in the helpdesk.
// Admins set tiers on the agent app's Customer organisations page; My Tickets looks
// the tier up when a ticket is raised and stores it on the ticket (ticket_tier), which
// the SLA automation rules read. A tier change applies to new tickets only.
//
// encatch: our own package and tables (encatch_ prefix, created at startup; not an
// upstream migration). Org ids are instance-prefixed ("prod-42"), see
// internal/encatch/mytickets/token.go.
package orgtiers

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// DefaultTier applies to orgs whose tier no admin has set.
const DefaultTier = "SaaS Standard"

var (
	ErrNotFound    = errors.New("organisation not found")
	ErrUnknownTier = errors.New("unknown support tier")
	ErrInvalid     = errors.New("invalid organisation")
	ErrExists      = errors.New("organisation already exists")

	instanceRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	numericRe  = regexp.MustCompile(`^[0-9]{1,18}$`)
)

const schema = `
CREATE TABLE IF NOT EXISTS encatch_orgs (
	org_id TEXT PRIMARY KEY,               -- instance-prefixed, e.g. prod-42
	instance TEXT NOT NULL,
	org_name TEXT NOT NULL DEFAULT '',
	tier TEXT NULL,                        -- NULL: not set, the default tier applies
	first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	last_seen_at TIMESTAMPTZ NULL,         -- last My Tickets sign-in or ticket
	tier_updated_at TIMESTAMPTZ NULL,
	tier_updated_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS encatch_orgs_instance ON encatch_orgs (instance);
CREATE TABLE IF NOT EXISTS encatch_org_tier_changes (
	id BIGSERIAL PRIMARY KEY,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	org_id TEXT NOT NULL,
	from_tier TEXT NULL,
	to_tier TEXT NOT NULL,
	actor_id INT NOT NULL,
	actor_name TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS encatch_org_tier_changes_org ON encatch_org_tier_changes (org_id, created_at DESC);`

// EnsureSchema creates the tables if needed. Idempotent; runs at startup.
func EnsureSchema(db *sqlx.DB) error {
	_, err := db.Exec(schema)
	return err
}

// Org is one customer organisation.
type Org struct {
	OrgID         string     `db:"org_id" json:"org_id"`
	Instance      string     `db:"instance" json:"instance"`
	OrgName       string     `db:"org_name" json:"org_name"`
	Tier          *string    `db:"tier" json:"tier"` // nil: not set
	EffectiveTier string     `db:"-" json:"effective_tier"`
	FirstSeenAt   time.Time  `db:"first_seen_at" json:"first_seen_at"`
	LastSeenAt    *time.Time `db:"last_seen_at" json:"last_seen_at"`
	TierUpdatedAt *time.Time `db:"tier_updated_at" json:"tier_updated_at"`
	TierUpdatedBy string     `db:"tier_updated_by" json:"tier_updated_by"`
	OpenTickets   int        `db:"open_tickets" json:"open_tickets"`
	Total         int        `db:"total" json:"-"`
}

// Change is one tier change.
type Change struct {
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	FromTier  *string   `db:"from_tier" json:"from_tier"`
	ToTier    string    `db:"to_tier" json:"to_tier"`
	ActorName string    `db:"actor_name" json:"actor_name"`
}

// Store reads and writes org tiers.
type Store struct {
	DB *sqlx.DB
	// Tiers lists valid tiers (my_tickets.tiers); the first-listed order is kept for display.
	Tiers []string
}

func (s *Store) effective(tier *string) string {
	if tier != nil && *tier != "" {
		return *tier
	}
	return DefaultTier
}

// Seen records that an org was used (My Tickets sign-in or ticket), keeping its name
// current, and returns its effective tier.
func (s *Store) Seen(instance, orgID, orgName string) (string, error) {
	if !instanceRe.MatchString(instance) || !strings.HasPrefix(orgID, instance+"-") {
		return "", ErrInvalid
	}
	var tier *string
	err := s.DB.Get(&tier, `INSERT INTO encatch_orgs (org_id, instance, org_name, last_seen_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (org_id) DO UPDATE SET org_name = EXCLUDED.org_name, last_seen_at = NOW()
		RETURNING tier`, orgID, instance, strings.TrimSpace(orgName))
	if err != nil {
		return "", fmt.Errorf("recording org: %w", err)
	}
	return s.effective(tier), nil
}

// Tier returns an org's effective tier (the default if unknown or not set).
func (s *Store) Tier(orgID string) (string, error) {
	var tier *string
	err := s.DB.Get(&tier, `SELECT tier FROM encatch_orgs WHERE org_id = $1`, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultTier, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading org tier: %w", err)
	}
	return s.effective(tier), nil
}

// List returns a page of orgs, newest activity first. search matches the name or the
// org id ("42" or "prod-42"); instance filters by instance when set.
func (s *Store) List(instance, search string, page, perPage int) ([]Org, int, error) {
	if perPage < 1 || perPage > 100 {
		perPage = 15
	}
	if page < 1 {
		page = 1
	}
	conds, args := []string{"TRUE"}, []any{perPage, (page - 1) * perPage}
	if instance != "" {
		args = append(args, instance)
		conds = append(conds, fmt.Sprintf("o.instance = $%d", len(args)))
	}
	if search = strings.TrimSpace(search); search != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(search) + "%"
		args = append(args, like, search)
		conds = append(conds, fmt.Sprintf("(o.org_name ILIKE $%d OR o.org_id = $%d OR split_part(o.org_id, '-', 2) = $%d)", len(args)-1, len(args), len(args)))
	}
	rows := []Org{}
	err := s.DB.Select(&rows, `SELECT o.org_id, o.instance, o.org_name, o.tier, o.first_seen_at, o.last_seen_at,
		o.tier_updated_at, o.tier_updated_by,
		(SELECT COUNT(*) FROM conversations c JOIN conversation_statuses st ON st.id = c.status_id
		 WHERE c.custom_attributes->>'org_id' = o.org_id AND st.name NOT IN ('Resolved', 'Closed')) AS open_tickets,
		COUNT(*) OVER () AS total
		FROM encatch_orgs o WHERE `+strings.Join(conds, " AND ")+`
		ORDER BY COALESCE(o.last_seen_at, o.first_seen_at) DESC, o.org_id LIMIT $1 OFFSET $2`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing orgs: %w", err)
	}
	total := 0
	for i := range rows {
		rows[i].EffectiveTier = s.effective(rows[i].Tier)
		total = rows[i].Total
	}
	return rows, total, nil
}

// SetTier changes an org's tier and records the change. It applies to new tickets only.
func (s *Store) SetTier(orgID, tier string, actorID int, actorName string) error {
	if !contains(s.Tiers, tier) {
		return ErrUnknownTier
	}
	tx, err := s.DB.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from *string
	if err := tx.Get(&from, `SELECT tier FROM encatch_orgs WHERE org_id = $1 FOR UPDATE`, orgID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if from != nil && *from == tier {
		return nil
	}
	if _, err := tx.Exec(`UPDATE encatch_orgs SET tier = $2, tier_updated_at = NOW(), tier_updated_by = $3 WHERE org_id = $1`,
		orgID, tier, actorName); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO encatch_org_tier_changes (org_id, from_tier, to_tier, actor_id, actor_name)
		VALUES ($1, $2, $3, $4, $5)`, orgID, from, tier, actorID, actorName); err != nil {
		return err
	}
	return tx.Commit()
}

// Add registers an org by hand (before its users have used My Tickets), optionally
// with a tier. orgNumber is the org's id within its instance.
func (s *Store) Add(instance, orgNumber, orgName, tier string, actorID int, actorName string) (string, error) {
	orgNumber = strings.TrimSpace(orgNumber)
	if !instanceRe.MatchString(instance) || !numericRe.MatchString(orgNumber) || strings.TrimSpace(orgName) == "" {
		return "", ErrInvalid
	}
	if tier != "" && !contains(s.Tiers, tier) {
		return "", ErrUnknownTier
	}
	orgID := instance + "-" + orgNumber
	res, err := s.DB.Exec(`INSERT INTO encatch_orgs (org_id, instance, org_name) VALUES ($1, $2, $3)
		ON CONFLICT (org_id) DO NOTHING`, orgID, instance, strings.TrimSpace(orgName))
	if err != nil {
		return "", fmt.Errorf("adding org: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return orgID, ErrExists
	}
	if tier != "" {
		if err := s.SetTier(orgID, tier, actorID, actorName); err != nil {
			return orgID, err
		}
	}
	return orgID, nil
}

// Changes returns an org's tier history, newest first.
func (s *Store) Changes(orgID string) ([]Change, error) {
	out := []Change{}
	err := s.DB.Select(&out, `SELECT created_at, from_tier, to_tier, actor_name FROM encatch_org_tier_changes
		WHERE org_id = $1 ORDER BY created_at DESC, id DESC LIMIT 100`, orgID)
	return out, err
}

// Instances lists the instances that have orgs, for the page's filter.
func (s *Store) Instances() ([]string, error) {
	out := []string{}
	err := s.DB.Select(&out, `SELECT DISTINCT instance FROM encatch_orgs ORDER BY instance`)
	return out, err
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
