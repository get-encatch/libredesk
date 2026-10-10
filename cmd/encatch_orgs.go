package main

// encatch: Customer organisations admin API: each Encatch org's support tier, which My
// Tickets puts on new tickets (internal/encatch/orgtiers). Called from initEncatch
// (cmd/encatch.go). Tiers decide SLAs, so every route needs sla:manage.

import (
	"errors"
	"strings"
	"sync"

	amodels "github.com/abhinavxd/libredesk/internal/auth/models"
	"github.com/abhinavxd/libredesk/internal/encatch/orgtiers"
	"github.com/abhinavxd/libredesk/internal/envelope"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

func initEncatchOrgs(g *fastglue.Fastglue) {
	var (
		once  sync.Once
		store *orgtiers.Store
	)
	get := func(app *App) *orgtiers.Store {
		once.Do(func() {
			db := initDB()
			db.SetMaxOpenConns(3)
			db.SetMaxIdleConns(1)
			if err := orgtiers.EnsureSchema(db); err != nil {
				app.lo.Error("orgs: creating org tier tables", "error", err)
			}
			store = &orgtiers.Store{DB: db, Tiers: ko.Strings("my_tickets.tiers")}
		})
		return store
	}
	h := func(fn func(*fastglue.Request, *App, *orgtiers.Store) error) fastglue.FastRequestHandler {
		return perm(func(r *fastglue.Request) error {
			app := r.Context.(*App)
			return fn(r, app, get(app))
		}, "sla:manage")
	}

	g.GET("/api/v1/encatch/orgs", h(handleListOrgs))
	g.GET("/api/v1/encatch/orgs/meta", h(handleOrgsMeta))
	g.POST("/api/v1/encatch/orgs", h(handleAddOrg))
	g.PUT("/api/v1/encatch/orgs/{org_id}/tier", h(handleSetOrgTier))
	g.GET("/api/v1/encatch/orgs/{org_id}/changes", h(handleOrgTierChanges))
}

func handleListOrgs(r *fastglue.Request, app *App, s *orgtiers.Store) error {
	q := r.RequestCtx.QueryArgs()
	perPage := q.GetUintOrZero("per_page")
	rows, total, err := s.List(string(q.Peek("instance")), string(q.Peek("search")), q.GetUintOrZero("page"), perPage)
	if err != nil {
		app.lo.Error("listing orgs", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Couldn't load organisations.", nil, envelope.GeneralError)
	}
	if perPage < 1 || perPage > 100 {
		perPage = 15
	}
	return r.SendEnvelope(map[string]any{"results": rows, "total": total, "per_page": perPage, "total_pages": max(1, (total+perPage-1)/perPage)})
}

// handleOrgsMeta returns what the page's filters and forms need.
func handleOrgsMeta(r *fastglue.Request, app *App, s *orgtiers.Store) error {
	instances, err := s.Instances()
	if err != nil {
		app.lo.Error("listing org instances", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Couldn't load organisations.", nil, envelope.GeneralError)
	}
	return r.SendEnvelope(map[string]any{"tiers": s.Tiers, "default_tier": orgtiers.DefaultTier, "instances": instances})
}

func handleSetOrgTier(r *fastglue.Request, app *App, s *orgtiers.Store) error {
	var req struct {
		Tier string `json:"tier"`
	}
	if err := r.Decode(&req, "json"); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request.", nil, envelope.InputError)
	}
	actor, err := orgsActor(r, app)
	if err != nil {
		return sendErrorEnvelope(r, err)
	}
	orgID := r.RequestCtx.UserValue("org_id").(string)
	if err := s.SetTier(orgID, req.Tier, actor.ID, actor.Name); err != nil {
		return orgsError(r, app, err)
	}
	app.lo.Info("org support tier changed", "org", orgID, "tier", req.Tier, "by", actor.ID)
	return r.SendEnvelope(true)
}

func handleAddOrg(r *fastglue.Request, app *App, s *orgtiers.Store) error {
	var req struct {
		Instance  string `json:"instance"`
		OrgNumber string `json:"org_number"`
		OrgName   string `json:"org_name"`
		Tier      string `json:"tier"`
	}
	if err := r.Decode(&req, "json"); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request.", nil, envelope.InputError)
	}
	actor, err := orgsActor(r, app)
	if err != nil {
		return sendErrorEnvelope(r, err)
	}
	orgID, err := s.Add(strings.ToLower(strings.TrimSpace(req.Instance)), req.OrgNumber, req.OrgName, req.Tier, actor.ID, actor.Name)
	if err != nil {
		return orgsError(r, app, err)
	}
	app.lo.Info("org added", "org", orgID, "tier", req.Tier, "by", actor.ID)
	return r.SendEnvelope(map[string]string{"org_id": orgID})
}

func handleOrgTierChanges(r *fastglue.Request, app *App, s *orgtiers.Store) error {
	changes, err := s.Changes(r.RequestCtx.UserValue("org_id").(string))
	if err != nil {
		app.lo.Error("listing org tier changes", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Couldn't load the history.", nil, envelope.GeneralError)
	}
	return r.SendEnvelope(changes)
}

type orgsActorInfo struct {
	ID   int
	Name string
}

func orgsActor(r *fastglue.Request, app *App) (orgsActorInfo, error) {
	auser := r.RequestCtx.UserValue("user").(amodels.User)
	user, err := app.user.GetAgentCachedOrLoad(auser.ID)
	if err != nil {
		return orgsActorInfo{}, err
	}
	return orgsActorInfo{ID: user.ID, Name: strings.TrimSpace(user.FullName())}, nil
}

func orgsError(r *fastglue.Request, app *App, err error) error {
	switch {
	case errors.Is(err, orgtiers.ErrNotFound):
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Organisation not found.", nil, envelope.NotFoundError)
	case errors.Is(err, orgtiers.ErrUnknownTier):
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Choose one of the support tiers.", nil, envelope.InputError)
	case errors.Is(err, orgtiers.ErrExists):
		return r.SendErrorEnvelope(fasthttp.StatusConflict, "This organisation is already listed. Change its tier in the list.", nil, envelope.ConflictError)
	case errors.Is(err, orgtiers.ErrInvalid):
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Enter the instance (e.g. prod), the org's number and its name.", nil, envelope.InputError)
	default:
		app.lo.Error("orgs", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Couldn't save. Please try again.", nil, envelope.GeneralError)
	}
}
