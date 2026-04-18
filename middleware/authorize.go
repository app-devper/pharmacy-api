package middleware

import (
	"net/http"
)

// roleLevel maps role name to numeric level. Higher = more privilege.
var roleLevel = map[string]int{
	"USER":  1,
	"ADMIN": 2,
	"SUPER": 3,
}

// RequireRole returns middleware that allows requests where the caller's role
// is >= minRole in the hierarchy (USER < ADMIN < SUPER).
func RequireRole(minRole string) func(http.Handler) http.Handler {
	min := roleLevel[minRole]
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := GetRole(r.Context())
			level, ok := roleLevel[role]
			if !ok || level < min {
				http.Error(w, `{"error":"insufficient permissions"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
