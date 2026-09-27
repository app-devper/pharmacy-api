package middleware

import (
	"net/http"

	"github.com/app-devper/um-api/sessionclient"
)

// RequireRole allows requests whose verified Principal is minRole or above in
// UM's ordering (USER < MANAGER < ADMIN < SUPER); an unknown role reaches
// nothing. It runs after a Verifier middleware.
func RequireRole(minRole string) func(http.Handler) http.Handler {
	return sessionclient.RequireRole(sessionclient.Role(minRole), RenderRefusal)
}
