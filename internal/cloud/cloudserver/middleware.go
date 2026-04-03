package cloudserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/Gentleman-Programming/engram/internal/cloud/auth"
)

// contextKey is an unexported type for context keys, preventing collisions.
type contextKey string

const userIDKey contextKey = "userID"

// withAuth is auth middleware that validates JWT or API key from the
// Authorization: Bearer header. On success it injects the userID into
// the request context. On failure it returns a 401 JSON error.
//
// Detection logic:
//   - Token starts with "eng_" -> API key flow (SHA-256 hash lookup)
//   - Otherwise -> JWT flow (HMAC-SHA256 validation)
func (s *CloudServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			jsonError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		var userID string

		if strings.HasPrefix(token, "eng_") {
			// API key flow
			user, err := auth.ValidateAPIKey(s.store, token)
			if err != nil {
				if isDBConnectionError(err) {
					jsonError(w, http.StatusServiceUnavailable, "database unavailable")
					return
				}
				jsonError(w, http.StatusUnauthorized, "invalid credentials")
				return
			}
			userID = user.ID
		} else {
			// JWT flow
			claims, err := s.auth.ValidateAccessToken(token)
			if err != nil {
				jsonError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			userID = claims.UserID
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next(w, r.WithContext(ctx))
	}
}

// getUserID extracts the authenticated userID from the request context.
// It assumes withAuth middleware has already run.
func getUserID(r *http.Request) string {
	v, _ := r.Context().Value(userIDKey).(string)
	return v
}

// extractBearerToken extracts the token from "Authorization: Bearer <token>".
func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
