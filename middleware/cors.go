package middleware

import (
	"net/http"
	"strings"
)

// CORS returns a middleware that reflects Origin when it matches one of the
// allowedOrigins entries. When allowedOrigins is empty (e.g. local development
// before FRONTEND_ORIGIN is set) it falls back to Allow-Origin: * — but only
// then, so production deployments that wire FRONTEND_ORIGIN get a real
// allowlist instead of a wildcard.
//
// Pass a comma-separated list via FRONTEND_ORIGIN, e.g.
//
//	FRONTEND_ORIGIN=https://dpharm.web.app,https://pos.devper.app
//
// Origins are matched case-insensitively after trimming whitespace.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	normalised := make([]string, 0, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o != "" {
			normalised = append(normalised, strings.ToLower(o))
		}
	}
	wildcard := len(normalised) == 0

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && originAllowed(origin, normalised) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(origin string, allowedLower []string) bool {
	o := strings.ToLower(origin)
	for _, a := range allowedLower {
		if a == o {
			return true
		}
	}
	return false
}

// ParseAllowedOrigins splits a comma-separated FRONTEND_ORIGIN env value
// into a slice of trimmed, non-empty entries. Returns an empty slice when
// the input is blank.
func ParseAllowedOrigins(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
