package user

// encatch: (get-encatch fork) agents change their own password (cmd/encatch_password.go).
// Upstream only lets an admin set an agent's password, or the agent reset it by email.

import (
	"github.com/abhinavxd/libredesk/internal/envelope"
	"github.com/abhinavxd/libredesk/internal/user/models"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// ChangeOwnPassword sets agent id's password to newPassword after checking current.
// It returns the agent (for the activity log) on success.
func (u *Manager) ChangeOwnPassword(id int, current, newPassword string) (models.User, error) {
	var user models.User
	if err := u.q.GetUser.Get(&user, id, "", pq.Array([]string{models.UserTypeAgent})); err != nil {
		u.lo.Error("encatch: fetching agent to change password", "user_id", id, "error", err)
		return user, envelope.NewError(envelope.GeneralError, u.i18n.T("globals.messages.somethingWentWrong"), nil)
	}
	if current == "" || !user.Password.Valid || u.verifyPassword([]byte(current), user.Password.String) != nil {
		return user, envelope.NewError(envelope.InputError, "Your current password is incorrect.", nil)
	}
	if newPassword == current {
		return user, envelope.NewError(envelope.InputError, "The new password must be different from the current one.", nil)
	}
	if !IsStrongPassword(newPassword) {
		return user, envelope.NewError(envelope.InputError, PasswordHint, nil)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		u.lo.Error("encatch: hashing new password", "user_id", id, "error", err)
		return user, envelope.NewError(envelope.GeneralError, u.i18n.T("globals.messages.somethingWentWrong"), nil)
	}
	if _, err := u.q.SetUserPassword.Exec(hash, id); err != nil {
		u.lo.Error("encatch: saving new password", "user_id", id, "error", err)
		return user, envelope.NewError(envelope.GeneralError, u.i18n.T("globals.messages.somethingWentWrong"), nil)
	}
	u.lo.Info("encatch: agent changed own password", "user_id", id)
	return user, nil
}
