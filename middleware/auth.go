package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/app-devper/um-api/sessionclient"
)

// UM tokens are verified by sessionclient's Verifier (um-api ADR-0005): the
// signature, expiry, system, client, and the live UM session, under the outage
// policy each route group chooses in routes.Setup. Handlers read the verified
// Principal from the request context.

// RenderRefusal writes a sessionclient refusal as {"code","error"}. Clients
// match the 503 message "identity service unavailable" to keep work pending.
func RenderRefusal(w http.ResponseWriter, _ *http.Request, e *sessionclient.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": e.Code, "error": e.Message})
}

func GetRole(ctx context.Context) string {
	p, _ := sessionclient.PrincipalFrom(ctx)
	return string(p.Role)
}

func GetClientID(ctx context.Context) string {
	p, _ := sessionclient.PrincipalFrom(ctx)
	return p.ClientID
}
