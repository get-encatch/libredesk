package main

// encatch: agents change their own password at Account > Password
// (frontend/apps/main/src/features/encatch/password). Called from initEncatch (cmd/encatch.go).
// Agents who sign in with SSO don't know their (random) libredesk password, so this is
// for password sign-ins; they can still set one through "Forgot password".

import (
	"net/http"

	amodels "github.com/abhinavxd/libredesk/internal/auth/models"
	"github.com/abhinavxd/libredesk/internal/envelope"
	realip "github.com/ferluci/fast-realip"
	"github.com/zerodha/fastglue"
)

type encatchChangePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func initEncatchPassword(g *fastglue.Fastglue) {
	// The auth rate limit (login, password reset) also caps guessing the current password.
	g.PUT("/api/v1/encatch/me/password", rateLimit(auth(handleEncatchChangePassword), "auth"))
}

func handleEncatchChangePassword(r *fastglue.Request) error {
	var (
		app  = r.Context.(*App)
		self = r.RequestCtx.UserValue("user").(amodels.User)
		req  encatchChangePasswordReq
	)
	if err := r.Decode(&req, "json"); err != nil {
		return r.SendErrorEnvelope(http.StatusBadRequest, app.i18n.T("errors.parsingRequest"), nil, envelope.InputError)
	}
	agent, err := app.user.ChangeOwnPassword(self.ID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		return sendErrorEnvelope(r, err)
	}
	app.user.InvalidateAgentCache(self.ID)
	if err := app.activityLog.PasswordSet(self.ID, agent.Email.String, realip.FromRequest(r.RequestCtx), self.ID, agent.Email.String); err != nil {
		app.lo.Error("encatch: recording password change", "user_id", self.ID, "error", err)
	}
	return r.SendEnvelope(true)
}
