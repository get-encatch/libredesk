package main

// encatch: wires the My Tickets customer pages (internal/encatch/mytickets) into
// libredesk. Called from initEncatch (cmd/encatch.go).

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/abhinavxd/libredesk/internal/encatch/mytickets"
	"github.com/abhinavxd/libredesk/internal/encatch/orgtiers"
	"github.com/jmoiron/sqlx"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Config (config.toml):
//
//	[my_tickets]
//	enabled = true
//	inbox_id = 1
//	session_ttl = "2h"
//	max_token_lifetime = "60s"
//	clock_leeway = "30s"
//	tiers = [...]           # valid support_tier values
//	priority_tiers = [...]  # tiers that may choose Express/Urgent
//	allowed_extensions = [...]  # attachment types customers may upload (also limited by libredesk's setting)
//	max_total_upload_mb = 50    # all attachments of one ticket or reply together
//
// Issuer secrets come from the environment only, never config files:
//
//	LIBREDESK_MY_TICKETS__ISSUERS__ENCATCH_ACCOUNTS_PROD="current-secret,previous-secret"
//
// (issuer "encatch-accounts-prod" -> key "encatch_accounts_prod"). Issuers must be
// encatch-accounts-<instance>; the helpdesk prefixes ids with the instance.
func initEncatchMyTickets(g *fastglue.Fastglue) {
	if !ko.Bool("my_tickets.enabled") {
		return
	}

	secrets := map[string][]string{}
	for key, val := range ko.StringMap("my_tickets.issuers") {
		var list []string
		for _, s := range strings.Split(val, ",") {
			if s = strings.TrimSpace(s); s != "" {
				list = append(list, s)
			}
		}
		if len(list) > 0 {
			secrets[mytickets.IssuerKey(key)] = list
		}
	}
	if len(secrets) == 0 {
		log.Printf("my-tickets: enabled but no issuer secrets configured; sign-in will refuse every token")
	}

	inboxID := ko.Int("my_tickets.inbox_id")
	sessionTTL := durationOr(ko.String("my_tickets.session_ttl"), 2*time.Hour)
	verifier := &mytickets.Verifier{
		Secrets:     secrets,
		MaxLifetime: durationOr(ko.String("my_tickets.max_token_lifetime"), 60*time.Second),
		Leeway:      durationOr(ko.String("my_tickets.clock_leeway"), 30*time.Second),
	}
	priorityTiers := ko.Strings("my_tickets.priority_tiers")
	customerExts := ko.Strings("my_tickets.allowed_extensions") // empty: libredesk's setting alone
	maxTotalMB := ko.Int("my_tickets.max_total_upload_mb")      // 0: no total limit

	// A small pool of our own for the list queries; libredesk doesn't expose its DB handle.
	var (
		db   *sqlx.DB
		once sync.Once
		svc  *mytickets.Service
	)
	get := func(app *App) *mytickets.Service {
		once.Do(func() {
			if db == nil {
				db = initDB()
				db.SetMaxOpenConns(5)
				db.SetMaxIdleConns(2)
			}
			if err := orgtiers.EnsureSchema(db); err != nil {
				app.lo.Error("my-tickets: creating org tier tables", "error", err)
			}
			s, err := mytickets.New(mytickets.Opts{
				Verifier: verifier,
				Store:    mytickets.NewStore(app.redis, sessionTTL),
				Backend: &mytickets.LibredeskBackend{
					Users: app.user, Conversations: app.conversation, Media: app.media, DB: db, InboxID: inboxID,
					OrgTiers: &orgtiers.Store{DB: db, Tiers: ko.Strings("my_tickets.tiers")},
				},
				EligibleTiers: priorityTiers,
				SessionTTL:    sessionTTL,
				Logger:        app.lo,
				// Attachments follow libredesk's upload settings (Admin > General), narrowed
				// to my_tickets.allowed_extensions: customers are less trusted than agents.
				UploadLimits: func() mytickets.UploadLimits {
					c := app.consts.Load().(*constants)
					return mytickets.UploadLimits{MaxMB: c.MaxFileUploadSizeMB, MaxTotalMB: maxTotalMB,
						Extensions: mytickets.IntersectExtensions(c.AllowedUploadFileExtensions, customerExts)}
				},
			})
			if err != nil {
				app.lo.Error("my-tickets: init failed", "error", err)
				return
			}
			svc = s
		})
		return svc
	}
	wrap := func(h func(*mytickets.Service, *fastglue.Request) error) fastglue.FastRequestHandler {
		return func(r *fastglue.Request) error {
			s := get(r.Context.(*App))
			if s == nil {
				r.RequestCtx.SetStatusCode(fasthttp.StatusServiceUnavailable)
				return nil
			}
			return h(s, r)
		}
	}

	g.GET("/my-tickets/assets/{file}", wrap((*mytickets.Service).Asset))
	g.GET("/my-tickets/login", rateLimit(wrap((*mytickets.Service).Login), "auth"))
	g.POST("/my-tickets/login", rateLimit(wrap((*mytickets.Service).Login), "auth"))
	g.POST("/my-tickets/logout", rateLimit(wrap((*mytickets.Service).Logout), "public"))
	g.GET("/my-tickets", rateLimit(wrap((*mytickets.Service).List), "public"))
	g.GET("/my-tickets/new", rateLimit(wrap((*mytickets.Service).NewForm), "public"))
	g.POST("/my-tickets/new", rateLimit(wrap((*mytickets.Service).Create), "public"))
	g.POST("/my-tickets/org", rateLimit(wrap((*mytickets.Service).SwitchOrg), "public"))
	g.GET("/my-tickets/{ref}", rateLimit(wrap((*mytickets.Service).View), "public"))
	g.POST("/my-tickets/{ref}/reply", rateLimit(wrap((*mytickets.Service).Reply), "public"))
}

func durationOr(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}
