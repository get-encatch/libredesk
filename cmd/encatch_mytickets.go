package main

// encatch: wires the My Tickets customer pages (internal/encatch/mytickets) into
// libredesk. Called from initEncatch (cmd/encatch.go).

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/abhinavxd/libredesk/internal/conversation"
	"github.com/abhinavxd/libredesk/internal/encatch/instancegate"
	"github.com/abhinavxd/libredesk/internal/encatch/mytickets"
	"github.com/abhinavxd/libredesk/internal/encatch/orgtiers"
	notifier "github.com/abhinavxd/libredesk/internal/notification"
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
//	email_instances = ["prod"]                            # customer emails
//	agent_notification_instances = ["dev", "uat", "prod"] # notifications to agents
//	ai_email_instances = []                               # AI agent emails: none
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
	// Per-instance gates: which Encatch instances' tickets send customer emails, agent
	// notifications and AI agent emails, so testing from local/dev/uat mails no one. An
	// absent setting gates nothing; an empty list allows no instance. Email tickets (no
	// instance) are never gated. Replies always show in My Tickets.
	var (
		gateOnce sync.Once
		gateDB   *sqlx.DB
	)
	gateDBFn := func() *sqlx.DB {
		gateOnce.Do(func() {
			gateDB = initDB()
			gateDB.SetMaxOpenConns(2)
			gateDB.SetMaxIdleConns(1)
		})
		return gateDB
	}
	gate := func(name, key string) *instancegate.Gate {
		g := &instancegate.Gate{Name: name, Enabled: ko.Exists(key), Instances: ko.Strings(key), DB: gateDBFn, Logf: log.Printf}
		if g.Enabled {
			log.Printf("my-tickets: %s only for tickets from instances %v", name, g.Instances)
		}
		return g
	}
	emailGate := gate("customer email", "my_tickets.email_instances")
	notifyGate := gate("agent notification", "my_tickets.agent_notification_instances")
	aiGate := gate("AI agent email", "my_tickets.ai_email_instances")
	conversation.SetEncatchEmailGates(emailGate.Allows, aiGate.Allows)
	notifier.SetEncatchNotifyGate(notifyGate.Allows)
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
			// The ticket list index; built without locking, in the background.
			go func() {
				if err := mytickets.EnsureIndexes(db); err != nil {
					app.lo.Error("my-tickets: creating ticket list index", "error", err)
				}
			}()
			s, err := mytickets.New(mytickets.Opts{
				Verifier: verifier,
				Store:    mytickets.NewStore(app.redis, sessionTTL),
				Backend: &mytickets.LibredeskBackend{
					Users: app.user, Conversations: app.conversation, Media: app.media, DB: db, InboxID: inboxID,
					OrgTiers: &orgtiers.Store{DB: db, Tiers: ko.Strings("my_tickets.tiers")}, Logger: app.lo,
					SubjectRefFormat: ko.String("conversation.subject_ref_format"),
					EmailGate:        emailGate,
					NotifyGate:       notifyGate,
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
