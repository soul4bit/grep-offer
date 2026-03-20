package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"grep-offer/internal/store"
)

func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "load users failed", http.StatusInternalServerError)
		return
	}

	rows := make([]AdminUserRow, 0, len(users))
	for _, user := range users {
		rows = append(rows, AdminUserRow{
			ID:            user.ID,
			Username:      user.Username,
			Email:         user.Email,
			IsAdmin:       user.IsAdmin,
			IsBanned:      user.IsBanned,
			IsCurrentUser: user.ID == currentUser.ID,
			CreatedLabel:  user.CreatedAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04"),
		})
	}

	a.render(w, r, http.StatusOK, "admin", ViewData{
		Notice:     noticeFromRequest(r),
		AdminUsers: rows,
	})
}

func (a *App) handleAdminUserAdmin(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	targetUser, ok := a.loadAdminTargetUser(w, r, currentUser)
	if !ok {
		return
	}

	makeAdmin, ok := parseAdminBool(r.FormValue("value"))
	if !ok {
		http.Error(w, "invalid admin flag", http.StatusBadRequest)
		return
	}

	if err := a.store.SetUserAdmin(r.Context(), targetUser.ID, makeAdmin); err != nil {
		http.Error(w, "save admin flag failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminUserBan(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	targetUser, ok := a.loadAdminTargetUser(w, r, currentUser)
	if !ok {
		return
	}

	banned, ok := parseAdminBool(r.FormValue("value"))
	if !ok {
		http.Error(w, "invalid ban flag", http.StatusBadRequest)
		return
	}

	if err := a.store.SetUserBanned(r.Context(), targetUser.ID, banned); err != nil {
		http.Error(w, "save ban flag failed", http.StatusInternalServerError)
		return
	}
	if banned {
		if err := a.store.DeleteSessionsByUserID(r.Context(), targetUser.ID); err != nil {
			http.Error(w, "drop user sessions failed", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	targetUser, ok := a.loadAdminTargetUser(w, r, currentUser)
	if !ok {
		return
	}

	if err := a.store.DeleteUser(r.Context(), targetUser.ID); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}

		http.Error(w, "delete user failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) *store.User {
	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return nil
	}
	if !user.IsAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil
	}
	return user
}

func (a *App) loadAdminTargetUser(w http.ResponseWriter, r *http.Request, currentUser *store.User) (*store.User, bool) {
	targetID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || targetID <= 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return nil, false
	}
	if targetID == currentUser.ID {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return nil, false
	}

	targetUser, err := a.store.UserByID(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			http.NotFound(w, r)
			return nil, false
		}
		http.Error(w, "load user failed", http.StatusInternalServerError)
		return nil, false
	}

	return targetUser, true
}

func (a *App) ensureBootstrapAdmin(ctx context.Context, user *store.User) (*store.User, error) {
	if user == nil || user.IsAdmin || len(a.bootstrapAdminEmails) == 0 {
		return user, nil
	}

	if _, ok := a.bootstrapAdminEmails[strings.ToLower(strings.TrimSpace(user.Email))]; !ok {
		return user, nil
	}

	if err := a.store.SetUserAdmin(ctx, user.ID, true); err != nil {
		return nil, err
	}

	return a.store.UserByID(ctx, user.ID)
}

func parseAdminBool(value string) (bool, bool) {
	switch strings.TrimSpace(value) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	default:
		return false, false
	}
}
