package middleware

import (
	"net/http"
)

func RequireRole(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := GetRole(r.Context())
			if role == "" {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			for _, a := range allowed {
				if role == a {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, `{"error":"insufficient permissions"}`, http.StatusForbidden)
		})
	}
}
