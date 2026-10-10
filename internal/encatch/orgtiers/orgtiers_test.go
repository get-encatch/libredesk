package orgtiers

// These tests need a libredesk database (List counts open tickets from its tables).
// They run in a throwaway schema, so existing data is untouched:
//
//	ENCATCH_TEST_DB='postgres://user:pass@127.0.0.1:5432/libredesk?sslmode=disable' go test ./internal/encatch/orgtiers/

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ENCATCH_TEST_DB")
	if dsn == "" {
		t.Skip("ENCATCH_TEST_DB not set")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("orgtiers_test_%d", time.Now().UnixNano())
	admin.MustExec("CREATE SCHEMA " + schema)
	t.Cleanup(func() {
		admin.MustExec("DROP SCHEMA " + schema + " CASCADE")
		admin.Close()
	})

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return &Store{DB: db, Tiers: []string{"SaaS Standard", "SaaS Growth", "Growth Plus"}}
}

func TestTiers(t *testing.T) {
	s := testStore(t)

	// Unknown orgs get the default tier.
	if tier, err := s.Tier("prod-1"); err != nil || tier != DefaultTier {
		t.Fatalf("unknown org: %q %v", tier, err)
	}

	// Seen registers the org, and checks the id belongs to the instance.
	if tier, err := s.Seen("prod", "prod-42", "BigCorp"); err != nil || tier != DefaultTier {
		t.Fatalf("seen: %q %v", tier, err)
	}
	if _, err := s.Seen("prod", "dev-42", "BigCorp"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("seen with another instance's id: %v", err)
	}

	// SetTier validates, applies and records the change.
	if err := s.SetTier("prod-42", "Gold", 1, "Admin"); !errors.Is(err, ErrUnknownTier) {
		t.Fatalf("unknown tier: %v", err)
	}
	if err := s.SetTier("prod-999", "SaaS Growth", 1, "Admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing org: %v", err)
	}
	if err := s.SetTier("prod-42", "Growth Plus", 1, "Admin"); err != nil {
		t.Fatal(err)
	}
	if tier, _ := s.Tier("prod-42"); tier != "Growth Plus" {
		t.Fatalf("tier after change: %q", tier)
	}
	if tier, _ := s.Seen("prod", "prod-42", "BigCorp Ltd"); tier != "Growth Plus" {
		t.Fatalf("seen keeps the tier: %q", tier)
	}
	changes, err := s.Changes("prod-42")
	if err != nil || len(changes) != 1 || changes[0].FromTier != nil || changes[0].ToTier != "Growth Plus" || changes[0].ActorName != "Admin" {
		t.Fatalf("changes: %+v %v", changes, err)
	}

	// Add: validation, unknown tiers leave nothing behind, existing orgs are refused.
	for _, c := range [][3]string{{"Prod!", "5", "X"}, {"prod", "4x", "X"}, {"prod", "5", " "}} {
		if _, err := s.Add(c[0], c[1], c[2], "", 1, "Admin"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("add %v: %v", c, err)
		}
	}
	if _, err := s.Add("prod", "5", "Acme", "Gold", 1, "Admin"); !errors.Is(err, ErrUnknownTier) {
		t.Fatalf("add with unknown tier: %v", err)
	}
	if _, total, _ := s.List("", "prod-5", 1, 15); total != 0 {
		t.Fatal("a failed add left the org behind")
	}
	if id, err := s.Add("prod", " 5 ", "Acme", "SaaS Growth", 1, "Admin"); err != nil || id != "prod-5" {
		t.Fatalf("add: %q %v", id, err)
	}
	if _, err := s.Add("prod", "42", "Renamed", "", 1, "Admin"); !errors.Is(err, ErrExists) {
		t.Fatalf("add existing: %v", err)
	}
	if _, err := s.Seen("dev", "dev-42", "BigCorp"); err != nil {
		t.Fatal(err)
	}

	// List: filters, search by name, number or full id, and the effective tier.
	rows, total, err := s.List("prod", "", 1, 15)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("list prod: %d %v", total, err)
	}
	for search, want := range map[string]int{"42": 2, "prod-42": 1, "acme": 1, "big": 2, "%": 0} {
		if _, n, err := s.List("", search, 1, 15); err != nil || n != want {
			t.Errorf("search %q: %d, want %d (%v)", search, n, want, err)
		}
	}
	rows, _, _ = s.List("dev", "", 1, 15)
	if len(rows) != 1 || rows[0].Tier != nil || rows[0].EffectiveTier != DefaultTier {
		t.Fatalf("dev org: %+v", rows)
	}
	if rows, total, _ := s.List("", "", 2, 2); total != 3 || len(rows) != 1 {
		t.Fatalf("paging: %d rows of %d", len(rows), total)
	}
	if inst, _ := s.Instances(); fmt.Sprint(inst) != "[dev prod]" {
		t.Fatalf("instances: %v", inst)
	}
}
