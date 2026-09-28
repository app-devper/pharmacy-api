package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/app-devper/um-api/servicekit/gateway"
)

// RequireGatewayHost refuses requests that did not come through the gateway
// (um-api servicekit, its ADR-0007), as {"error": ...} like every handler.
func RequireGatewayHost(allowedHosts string) func(http.Handler) http.Handler {
	return gateway.Middleware(gateway.ParseHosts(allowedHosts), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": gateway.Message})
	})
}
