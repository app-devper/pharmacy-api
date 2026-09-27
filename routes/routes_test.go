package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// ADR-0001: only ordinary catalog reads may run while UM cannot confirm the
// session. Everything else must be refused.
var catalogReads = map[string]bool{
	"GET /api/pharmacy/v1/drugs":           true,
	"GET /api/pharmacy/v1/drugs/low-stock": true,
	"GET /api/pharmacy/v1/drugs/{id}/lots": true,
	"GET /api/pharmacy/v1/lots/expiring":   true,
}

func TestUMOutageRefusesEverythingButCatalogReads(t *testing.T) {
	r := setupRouter(t, downUM{})
	token := signedToken(t, "ADMIN")

	seen := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if catalogReads[key] {
			seen[key] = true
			return nil
		}
		path := samplePath(route)
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: expected 503 while UM is down, got %d", key, rec.Code)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(seen) != len(catalogReads) {
		t.Fatalf("expected all catalog reads registered, saw %v", seen)
	}
}
