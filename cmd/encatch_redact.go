package main

// encatch: routes for removing sensitive content agents or customers shared by
// mistake (internal/encatch/redact). Called from initEncatch (cmd/encatch.go).

import (
	"errors"
	"strings"
	"sync"
	"time"

	amodels "github.com/abhinavxd/libredesk/internal/auth/models"
	"github.com/abhinavxd/libredesk/internal/encatch/redact"
	"github.com/abhinavxd/libredesk/internal/envelope"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

func initEncatchRedact(g *fastglue.Fastglue) {
	var (
		once sync.Once
		svc  *redact.Service
	)
	get := func(app *App) *redact.Service {
		once.Do(func() {
			db := initDB()
			db.SetMaxOpenConns(3)
			db.SetMaxIdleConns(1)
			if err := redact.EnsureSchema(db); err != nil {
				app.lo.Error("redact: creating encatch_redactions table", "error", err)
			}
			svc = &redact.Service{DB: db, Conversations: app.conversation, Media: app.media, Lo: app.lo}
			// Files of deleted private notes go now, not after libredesk's 7 days.
			go svc.PurgeDeletedNoteFiles(app.ctx, time.Minute)
		})
		return svc
	}
	// Start the purge job with the first request of any kind, so it runs even
	// if nobody uses the redact endpoints. fastglue has no way to read the app
	// context at registration time.
	g.Before(func(r *fastglue.Request) *fastglue.Request {
		if app, ok := r.Context.(*App); ok {
			get(app)
		}
		return r
	})

	// The removal log is visible to whoever can see libredesk's activity log (admins by default).
	g.GET("/api/v1/encatch/redactions", perm(func(r *fastglue.Request) error {
		var (
			app     = r.Context.(*App)
			q       = r.RequestCtx.QueryArgs()
			page    = q.GetUintOrZero("page")
			perPage = q.GetUintOrZero("per_page")
		)
		rows, total, err := get(app).List(string(q.Peek("search")), page, perPage)
		if err != nil {
			app.lo.Error("listing removal log", "error", err)
			return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Couldn't load the removal log.", nil, envelope.GeneralError)
		}
		if perPage < 1 || perPage > redact.MaxPerPage {
			perPage = 20
		}
		return r.SendEnvelope(map[string]any{
			"results": rows, "total": total, "per_page": perPage, "total_pages": (total + perPage - 1) / perPage,
		})
	}, "activity_logs:manage"))

	g.POST("/api/v1/encatch/conversations/{cuuid}/messages/{uuid}/remove-attachment",
		perm(func(r *fastglue.Request) error { return handleRedact(r, get, true) }, "messages:write"))
	g.POST("/api/v1/encatch/conversations/{cuuid}/messages/{uuid}/remove-text",
		perm(func(r *fastglue.Request) error { return handleRedact(r, get, false) }, "messages:write"))
}

func handleRedact(r *fastglue.Request, get func(*App) *redact.Service, attachment bool) error {
	var (
		app   = r.Context.(*App)
		cuuid = r.RequestCtx.UserValue("cuuid").(string)
		uuid  = r.RequestCtx.UserValue("uuid").(string)
		auser = r.RequestCtx.UserValue("user").(amodels.User)
		req   struct {
			MediaUUID string `json:"media_uuid"`
			Reason    string `json:"reason"`
		}
	)
	user, err := app.user.GetAgentCachedOrLoad(auser.ID)
	if err != nil {
		return sendErrorEnvelope(r, err)
	}
	if _, err := enforceConversationAccess(app, cuuid, user); err != nil {
		return sendErrorEnvelope(r, err)
	}
	if err := r.Decode(&req, "json"); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request.", nil, envelope.InputError)
	}
	actor := redact.Actor{ID: user.ID, Name: strings.TrimSpace(user.FullName()), IsAdmin: user.HasAdminRole()}
	var res redact.Result
	if attachment {
		if req.MediaUUID == "" {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Choose an attachment to remove.", nil, envelope.InputError)
		}
		res, err = get(app).RemoveAttachment(actor, cuuid, uuid, req.MediaUUID, req.Reason)
	} else {
		res, err = get(app).RemoveText(actor, cuuid, uuid, req.Reason)
	}
	switch {
	case err == nil:
		return r.SendEnvelope(res)
	case errors.Is(err, redact.ErrNotFound):
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, err.Error(), nil, envelope.NotFoundError)
	case errors.Is(err, redact.ErrForbidden):
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, err.Error(), nil, envelope.PermissionError)
	case errors.Is(err, redact.ErrNotAllowed), errors.Is(err, redact.ErrReason):
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, envelope.InputError)
	default:
		app.lo.Error("redact failed", "conversation", cuuid, "message", uuid, "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Couldn't remove it. Please try again.", nil, envelope.GeneralError)
	}
}
