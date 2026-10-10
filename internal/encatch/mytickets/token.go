// Package mytickets serves the Encatch customer page at /my-tickets: customers arrive
// with a short-lived signed token from an Encatch backend app, get their own session
// (separate from agent sessions) and can list, view, raise and reply to tickets.
//
// encatch: this package is ours, not upstream libredesk. Everything it needs from
// libredesk goes through the Backend interface (adapter.go), so upstream changes
// surface in one place.
package mytickets

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Scope values in the token.
const (
	ScopeSelf    = "self"
	ScopeProject = "project"
	ScopeOrg     = "org"
)

var (
	ErrUnknownIssuer = errors.New("unknown issuer")
	ErrBadToken      = errors.New("invalid token")
	ErrTokenTooLong  = errors.New("token lifetime too long")
)

// ID accepts a JSON string or number (Encatch IDs are auto-increment integers, but
// issuers may send them either way) and keeps it as a string.
type ID string

func (i *ID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*i = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*i = ID(strings.TrimSpace(v))
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("id must be a string or number: %w", err)
	}
	if _, err := strconv.ParseInt(n.String(), 10, 64); err != nil {
		return fmt.Errorf("id must be an integer: %w", err)
	}
	*i = ID(n.String())
	return nil
}

// Project is one project the user may see.
type Project struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// Claims is the token payload agreed in the design doc.
type Claims struct {
	ExternalUserID   ID        `json:"external_user_id"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	OrgID            ID        `json:"org_id"`
	OrgName          string    `json:"org_name"`
	SupportTier      string    `json:"support_tier"`
	Projects         []Project `json:"projects"`
	CurrentProjectID ID        `json:"current_project_id"`
	Scope            string    `json:"scope"`
	jwt.RegisteredClaims
}

// Verifier checks tokens against per-issuer secrets.
type Verifier struct {
	// Secrets maps issuer (as normalised by IssuerKey) to accepted secrets, current first.
	Secrets map[string][]string
	// MaxLifetime is the longest allowed exp - iat.
	MaxLifetime time.Duration
	// Leeway tolerates clock skew between issuer and libredesk.
	Leeway time.Duration
	// ValidTiers lists accepted support_tier values; others are dropped.
	ValidTiers []string
}

// IssuerKey normalises an issuer for config lookup: env var names can't hold "-".
func IssuerKey(iss string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(iss)), "-", "_")
}

// Verify parses and validates a token, returning its claims. It does not check
// one-time use; the caller records the jti.
func (v *Verifier) Verify(token string) (*Claims, error) {
	// Read the issuer before verifying, only to choose the secrets.
	var peek jwt.RegisteredClaims
	if _, _, err := jwt.NewParser().ParseUnverified(token, &peek); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadToken, err)
	}
	secrets := v.Secrets[IssuerKey(peek.Issuer)]
	if peek.Issuer == "" || len(secrets) == 0 {
		return nil, ErrUnknownIssuer
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(peek.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(v.Leeway),
	)
	var lastErr error
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		var c Claims
		_, err := parser.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return []byte(secret), nil })
		if err != nil {
			lastErr = err
			if errors.Is(err, jwt.ErrTokenSignatureInvalid) {
				continue // try the previous secret
			}
			return nil, fmt.Errorf("%w: %v", ErrBadToken, err)
		}
		if err := v.check(&c); err != nil {
			return nil, err
		}
		return &c, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrBadToken, lastErr)
}

func (v *Verifier) check(c *Claims) error {
	if c.IssuedAt == nil || c.ExpiresAt == nil {
		return fmt.Errorf("%w: iat and exp are required", ErrBadToken)
	}
	if c.ExpiresAt.Sub(c.IssuedAt.Time) > v.MaxLifetime {
		return ErrTokenTooLong
	}
	if c.ID == "" {
		return fmt.Errorf("%w: jti is required", ErrBadToken)
	}
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	if c.ExternalUserID == "" || c.Email == "" || !strings.Contains(c.Email, "@") {
		return fmt.Errorf("%w: external_user_id and a valid email are required", ErrBadToken)
	}
	if c.OrgID == "" || strings.TrimSpace(c.OrgName) == "" {
		return fmt.Errorf("%w: org_id and org_name are required", ErrBadToken)
	}
	switch c.Scope {
	case "":
		c.Scope = ScopeSelf
	case ScopeSelf, ScopeProject, ScopeOrg:
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrBadToken, c.Scope)
	}
	if c.SupportTier != "" && !contains(v.ValidTiers, c.SupportTier) {
		c.SupportTier = "" // unknown tier: fall back to the contact's tier
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
