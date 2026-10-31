package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"smart-transit/internal/auth"
)

const sessionCookie = "st_session"

type ctxKey int

const userKey ctxKey = 1

func userFrom(ctx context.Context) auth.User {
	u, _ := ctx.Value(userKey).(auth.User)
	return u
}

// requireOperator authenticates the session cookie and checks the role
// (plan 13: authenticate operators, role-based authorization).
func (h *handlers) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "operator login required")
			return
		}
		u, err := h.deps.Auth.Authenticate(r.Context(), c.Value)
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "session expired; please log in again")
			return
		}
		if err != nil {
			writeInternal(w, r, err)
			return
		}
		if !u.CanManageAlerts() {
			writeError(w, http.StatusForbidden, "forbidden", "operator role required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *handlers) setSessionCookie(w http.ResponseWriter, value string, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.deps.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	token, user, expires, err := h.deps.Auth.Login(r.Context(), req.Email, req.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	h.setSessionCookie(w, token, expires, int(time.Until(expires).Seconds()))
	writeData(w, http.StatusOK, map[string]any{"user": user, "expires_at": expires})
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := h.deps.Auth.Logout(r.Context(), c.Value); err != nil {
			writeInternal(w, r, err)
			return
		}
	}
	h.setSessionCookie(w, "", time.Unix(0, 0), -1)
	writeData(w, http.StatusOK, map[string]bool{"logged_out": true})
}

func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, userFrom(r.Context()))
}
