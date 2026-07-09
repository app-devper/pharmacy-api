package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func gatewayRequest(t *testing.T, allowedHosts, forwardedHost string) *httptest.ResponseRecorder {
	t.Helper()
	handler := RequireGatewayHost(allowedHosts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/pharmacy/v1/drugs", nil)
	if forwardedHost != "" {
		req.Header.Set("X-Forwarded-Host", forwardedHost)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestGatewayHostDisabledPassesWithoutHeader(t *testing.T) {
	rec := gatewayRequest(t, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestGatewayHostAllowsMatchingForwardedHost(t *testing.T) {
	rec := gatewayRequest(t, "api.devper.app", "api.devper.app")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestGatewayHostRejectsMissingHeader(t *testing.T) {
	rec := gatewayRequest(t, "api.devper.app", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestGatewayHostRejectsUnknownHost(t *testing.T) {
	rec := gatewayRequest(t, "api.devper.app", "evil.example.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestGatewayHostUsesFirstForwardedValue(t *testing.T) {
	rec := gatewayRequest(t, "api.devper.app", "api.devper.app, cdn.internal")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestGatewayHostMatchesCaseInsensitive(t *testing.T) {
	rec := gatewayRequest(t, "api.devper.app", "API.Devper.App")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestGatewayHostAllowsAnyConfiguredHost(t *testing.T) {
	rec := gatewayRequest(t, "api.devper.app, devper-api.web.app", "devper-api.web.app")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
