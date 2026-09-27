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
)

const testSecret = "test-secret"

// liveUM confirms every session; downUM cannot answer.
type liveUM struct{}

func (liveUM) Session(context.Context, string) (sessionclient.Session, error) {
	return sessionclient.Session{UserId: "u1", System: "PHARMACY"}, nil
}

type downUM struct{}

func (downUM) Session(context.Context, string) (sessionclient.Session, error) {
	return sessionclient.Session{}, sessionclient.ErrUnavailable
}

// setupRouter builds the real router over UM's session store and handlers
// without a database.
func setupRouter(t *testing.T, store sessionclient.Store) *chi.Mux {
	t.Helper()
	identity, err := sessionclient.NewVerifier(sessionclient.Config{SecretKey: testSecret, System: "PHARMACY", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	return Setup(
		&handlers.DrugHandler{}, &handlers.DrugLotHandler{}, &handlers.CustomerHandler{},
		&handlers.SaleHandler{}, &handlers.ReportHandler{}, &handlers.KyHandler{},
		&handlers.ExportHandler{}, &handlers.ImportHandler{}, &handlers.SupplierHandler{},
		&handlers.StockAdjustmentHandler{}, &handlers.StockCountHandler{}, &handlers.ReturnHandler{},
		&handlers.MovementsHandler{}, &handlers.SettingsHandler{}, &handlers.LabelHandler{},
		nil, "", identity,
	)
}

func signedToken(t *testing.T, role string) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": role, "system": "PHARMACY", "clientId": "123", "jti": "s1",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func samplePath(route string) string {
	return strings.NewReplacer("{id}", "0123456789abcdef01234567", "{lot_id}", "0123456789abcdef01234567", "{form}", "ky9").Replace(route)
}

func TestEveryRouteRejectsAnAnonymousRequest(t *testing.T) {
	r := setupRouter(t, liveUM{})
	checked := 0
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		checked++
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, samplePath(route), nil))
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), sessionclient.CodeMissingToken) {
			t.Errorf("%s %s: expected 401 %s without a token, got %d %s", method, route, sessionclient.CodeMissingToken, rec.Code, rec.Body.String())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("no routes registered")
	}
}
