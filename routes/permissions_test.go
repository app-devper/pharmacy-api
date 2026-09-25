package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"pharmacy-pos/backend/handlers"
	mw "pharmacy-pos/backend/middleware"
)

// minimumRole is the shared role policy (KMP ADR-0004) route by route. Every
// registered route must appear here, so a new route cannot ship without a
// deliberate permission.
var minimumRole = map[string]string{
	// USER+: catalog, selling, returns, customers at the counter
	"GET /api/pharmacy/v1/drugs":                RoleUSER,
	"GET /api/pharmacy/v1/drugs/low-stock":      RoleUSER,
	"GET /api/pharmacy/v1/drugs/{id}/lots":      RoleUSER,
	"GET /api/pharmacy/v1/lots/expiring":        RoleUSER,
	"GET /api/pharmacy/v1/customers":            RoleUSER,
	"POST /api/pharmacy/v1/customers":           RoleUSER,
	"GET /api/pharmacy/v1/customers/{id}/sales": RoleUSER,
	"GET /api/pharmacy/v1/sales":                RoleUSER,
	"POST /api/pharmacy/v1/sales":               RoleUSER,
	"GET /api/pharmacy/v1/sales/{id}/items":     RoleUSER,
	"GET /api/pharmacy/v1/sales/{id}/ky":        RoleUSER,
	"POST /api/pharmacy/v1/sales/{id}/return":   RoleUSER,
	"GET /api/pharmacy/v1/sales/{id}/returns":   RoleUSER,
	"GET /api/pharmacy/v1/movements":            RoleUSER,
	"GET /api/pharmacy/v1/settings":             RoleUSER,

	// MANAGER+: stock counts/adjustments/lots, goods receipt, suppliers,
	// customer edits, labels, slow-drugs
	"GET /api/pharmacy/v1/drugs/reorder-suggestions":   RoleMANAGER,
	"POST /api/pharmacy/v1/drugs/{id}/adjustments":     RoleMANAGER,
	"GET /api/pharmacy/v1/drugs/{id}/adjustments":      RoleMANAGER,
	"GET /api/pharmacy/v1/stock-counts":                RoleMANAGER,
	"POST /api/pharmacy/v1/stock-counts":               RoleMANAGER,
	"POST /api/pharmacy/v1/drugs/{id}/lots":            RoleMANAGER,
	"DELETE /api/pharmacy/v1/drugs/{id}/lots/{lot_id}": RoleMANAGER,
	"POST /api/pharmacy/v1/lots/writeoff":              RoleMANAGER,
	"GET /api/pharmacy/v1/imports":                     RoleMANAGER,
	"POST /api/pharmacy/v1/imports":                    RoleMANAGER,
	"GET /api/pharmacy/v1/imports/{id}":                RoleMANAGER,
	"PUT /api/pharmacy/v1/imports/{id}":                RoleMANAGER,
	"POST /api/pharmacy/v1/imports/{id}/confirm":       RoleMANAGER,
	"DELETE /api/pharmacy/v1/imports/{id}":             RoleMANAGER,
	"GET /api/pharmacy/v1/suppliers":                   RoleMANAGER,
	"POST /api/pharmacy/v1/suppliers":                  RoleMANAGER,
	"PUT /api/pharmacy/v1/suppliers/{id}":              RoleMANAGER,
	"DELETE /api/pharmacy/v1/suppliers/{id}":           RoleMANAGER,
	"PUT /api/pharmacy/v1/customers/{id}":              RoleMANAGER,
	"POST /api/pharmacy/v1/labels/print":               RoleMANAGER,
	"GET /api/pharmacy/v1/report/slow-drugs":           RoleMANAGER,

	// ADMIN+: drug identity and price, whole-bill void, reports, KY, settings
	"POST /api/pharmacy/v1/drugs":           RoleADMIN,
	"POST /api/pharmacy/v1/drugs/bulk":      RoleADMIN,
	"PUT /api/pharmacy/v1/drugs/{id}":       RoleADMIN,
	"POST /api/pharmacy/v1/sales/{id}/void": RoleADMIN,
	"GET /api/pharmacy/v1/report/summary":   RoleADMIN,
	"GET /api/pharmacy/v1/report/dashboard": RoleADMIN,
	"GET /api/pharmacy/v1/report/daily":     RoleADMIN,
	"GET /api/pharmacy/v1/report/monthly":   RoleADMIN,
	"GET /api/pharmacy/v1/report/top-drugs": RoleADMIN,
	"GET /api/pharmacy/v1/report/eod":       RoleADMIN,
	"GET /api/pharmacy/v1/report/profit":    RoleADMIN,
	"GET /api/pharmacy/v1/ky9":              RoleADMIN,
	"POST /api/pharmacy/v1/ky9":             RoleADMIN,
	"GET /api/pharmacy/v1/ky10":             RoleADMIN,
	"POST /api/pharmacy/v1/ky10":            RoleADMIN,
	"GET /api/pharmacy/v1/ky11":             RoleADMIN,
	"POST /api/pharmacy/v1/ky11":            RoleADMIN,
	"GET /api/pharmacy/v1/ky12":             RoleADMIN,
	"POST /api/pharmacy/v1/ky12":            RoleADMIN,
	"GET /api/pharmacy/v1/export/{form}":    RoleADMIN,
	"PUT /api/pharmacy/v1/settings":         RoleADMIN,
}

var roleOrder = []string{RoleUSER, RoleMANAGER, RoleADMIN, RoleSUPER}

func rank(role string) int {
	for i, r := range roleOrder {
		if r == role {
			return i
		}
	}
	return -1
}

func TestEveryRouteEnforcesItsMinimumRole(t *testing.T) {
	// Live identity disabled: this test is only about role permissions.
	r := Setup(
		&handlers.DrugHandler{}, &handlers.DrugLotHandler{}, &handlers.CustomerHandler{},
		&handlers.SaleHandler{}, &handlers.ReportHandler{}, &handlers.KyHandler{},
		&handlers.ExportHandler{}, &handlers.ImportHandler{}, &handlers.SupplierHandler{},
		&handlers.StockAdjustmentHandler{}, &handlers.StockCountHandler{}, &handlers.ReturnHandler{},
		&handlers.MovementsHandler{}, &handlers.SettingsHandler{}, &handlers.LabelHandler{},
		"test-secret", "PHARMACY", nil, "", mw.NewLiveIdentity(nil),
	)

	registered := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		registered[key] = true
		minimum, ok := minimumRole[key]
		if !ok {
			t.Errorf("%s has no entry in minimumRole; decide its permission", key)
			return nil
		}
		path := strings.NewReplacer("{id}", "0123456789abcdef01234567", "{lot_id}", "0123456789abcdef01234567", "{form}", "ky9").Replace(route)
		for _, role := range append(roleOrder, "CASHIER") {
			code := serveAs(t, r, method, path, role)
			allowed := rank(role) >= rank(minimum) && rank(role) >= 0
			if allowed && code == http.StatusForbidden {
				t.Errorf("%s as %s: denied, want allowed (minimum %s)", key, role, minimum)
			}
			if !allowed && code != http.StatusForbidden {
				t.Errorf("%s as %s: got %d, want 403 (minimum %s)", key, role, code, minimum)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for key := range minimumRole {
		if !registered[key] {
			t.Errorf("%s is in minimumRole but not registered", key)
		}
	}
}

// serveAs returns the status for a request by a signed-in role. Allowed
// requests reach handlers without a database, which fail with a non-403
// status; that is enough to tell allowed from denied.
func serveAs(t *testing.T, r http.Handler, method, path, role string) int {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &mw.AccessClaims{
		Role: role, System: "PHARMACY", ClientId: "123",
		RegisteredClaims: jwt.RegisteredClaims{ID: "s1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}
