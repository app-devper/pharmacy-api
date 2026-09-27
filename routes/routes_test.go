package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/app-devper/um-api/sessionclient"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"pharmacy-pos/backend/handlers"
	mw "pharmacy-pos/backend/middleware"
)

// ADR-0001: only ordinary catalog reads may run while UM cannot confirm the
// session. Everything else must be refused.
var catalogReads = map[string]bool{
	"GET /api/pharmacy/v1/drugs":           true,
	"GET /api/pharmacy/v1/drugs/low-stock": true,
	"GET /api/pharmacy/v1/drugs/{id}/lots": true,
	"GET /api/pharmacy/v1/lots/expiring":   true,
}

type downUM struct{}

func (downUM) Session(context.Context, string) (sessionclient.Session, error) {
	return sessionclient.Session{}, sessionclient.ErrUnavailable
}

func TestUMOutageRefusesEverythingButCatalogReads(t *testing.T) {
	r := Setup(
		&handlers.DrugHandler{}, &handlers.DrugLotHandler{}, &handlers.CustomerHandler{},
		&handlers.SaleHandler{}, &handlers.ReportHandler{}, &handlers.KyHandler{},
		&handlers.ExportHandler{}, &handlers.ImportHandler{}, &handlers.SupplierHandler{},
		&handlers.StockAdjustmentHandler{}, &handlers.StockCountHandler{}, &handlers.ReturnHandler{},
		&handlers.MovementsHandler{}, &handlers.SettingsHandler{}, &handlers.LabelHandler{},
		"test-secret", "PHARMACY", nil, "", mw.NewLiveIdentity(sessionclient.NewChecker(downUM{})),
	)
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, &mw.AccessClaims{
		Role: "ADMIN", System: "PHARMACY", ClientId: "123",
		RegisteredClaims: jwt.RegisteredClaims{ID: "s1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString([]byte("test-secret"))

	seen := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if catalogReads[key] {
			seen[key] = true
			return nil
		}
		path := strings.NewReplacer("{id}", "0123456789abcdef01234567", "{lot_id}", "0123456789abcdef01234567", "{form}", "ky9").Replace(route)
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
