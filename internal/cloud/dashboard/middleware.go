package dashboard

import (
	"context"
	"net/http"

	"github.com/Gentleman-Programming/engram/internal/cloud/auth"
)

// sessionCookieName is the name of the HTTP-only cookie that stores the JWT
// access token for browser sessions.
const sessionCookieName = "engram_session"

// contextKey is an unexported type for context keys, preventing collisions
// with other packages.
type contextKey string

const (
	ctxUserID   contextKey = "dashboard_userID"
	ctxUsername contextKey = "dashboard_username"
	ctxEmail    contextKey = "dashboard_email"
)

// withCookieAuth is middleware that extracts a JWT from the engram_session
// cookie, validates it via auth.Service, and injects user info into the
// request context. On failure it redirects to the login page.
//
// This does NOT affect the API auth flow — it only applies to dashboard routes.
func withCookieAuth(authSvc *auth.Service, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
			return
		}

		claims, err := authSvc.ValidateAccessToken(cookie.Value)
		if err != nil {
			// Invalid or expired token — clear cookie and redirect to login.
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    "",
				Path:     "/dashboard",
				MaxAge:   -1,
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, ctxUserID, claims.UserID)
		ctx = context.WithValue(ctx, ctxUsername, claims.Username)
		// Email is not in JWT claims — we store username for now.
		// To get email we'd need a store lookup, which we can add later.
		ctx = context.WithValue(ctx, ctxEmail, "")

		next(w, r.WithContext(ctx))
	}
}

// getUserIDFromContext extracts the authenticated user ID from the request context.
func getUserIDFromContext(r *http.Request) string {
	v, _ := r.Context().Value(ctxUserID).(string)
	return v
}

// getUsernameFromContext extracts the authenticated username from the request context.
func getUsernameFromContext(r *http.Request) string {
	v, _ := r.Context().Value(ctxUsername).(string)
	return v
}

// getEmailFromContext extracts the authenticated user's email from the request context.
func getEmailFromContext(r *http.Request) string {
	v, _ := r.Context().Value(ctxEmail).(string)
	return v
}
