package middleware

import (
	"net/http"
	"strings"
)

func RequireGatewayHost(allowedHosts string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, host := range strings.Split(allowedHosts, ",") {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			allowed[host] = true
		}
	}
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
			if !allowed[strings.ToLower(forwarded)] {
				http.Error(w, `{"error":"direct access is not allowed"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
