package middleware

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func runCORS(t *testing.T, allowed []string, method, origin string) http.Header {
	t.Helper()
	called := false
	handler := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(method, "/anything", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if method != http.MethodOptions && !called {
		t.Fatalf("CORS should chain to next handler on %s", method)
	}
	if method == http.MethodOptions && called {
		t.Fatalf("CORS should short-circuit OPTIONS preflight without calling next")
	}
	return rr.Result().Header
}

func TestCORS_NoAllowlist_FallsBackToWildcard(t *testing.T) {
	h := runCORS(t, nil, http.MethodGet, "https://malicious.example")
	if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected wildcard for empty allowlist, got %q", got)
	}
}

func TestCORS_AllowedOrigin_IsReflected(t *testing.T) {
	allowed := []string{"https://dpharm.web.app", "https://pos.devper.app"}
	h := runCORS(t, allowed, http.MethodGet, "https://dpharm.web.app")
	if got := h.Get("Access-Control-Allow-Origin"); got != "https://dpharm.web.app" {
		t.Errorf("expected allowed origin reflected, got %q", got)
	}
	if got := h.Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin to be set when reflecting, got %q", got)
	}
}

func TestCORS_DisallowedOrigin_HasNoOriginHeader(t *testing.T) {
	allowed := []string{"https://dpharm.web.app"}
	h := runCORS(t, allowed, http.MethodGet, "https://attacker.example")
	if got := h.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no Allow-Origin for disallowed origin, got %q", got)
	}
}

func TestCORS_OriginMatchIsCaseInsensitive(t *testing.T) {
	allowed := []string{"https://Dpharm.Web.App"}
	h := runCORS(t, allowed, http.MethodGet, "https://dpharm.web.app")
	if got := h.Get("Access-Control-Allow-Origin"); got != "https://dpharm.web.app" {
		t.Errorf("expected case-insensitive match, got %q", got)
	}
}

func TestCORS_AllowlistWithBlankEntriesIsIgnored(t *testing.T) {
	allowed := []string{"", "  ", "https://dpharm.web.app", ""}
	h := runCORS(t, allowed, http.MethodGet, "https://dpharm.web.app")
	if got := h.Get("Access-Control-Allow-Origin"); got != "https://dpharm.web.app" {
		t.Errorf("blank entries should be stripped, got %q", got)
	}
}

func TestCORS_AllowedHeadersAndMethodsAlwaysSet(t *testing.T) {
	allowed := []string{"https://dpharm.web.app"}
	for _, origin := range []string{"https://dpharm.web.app", "https://attacker.example", ""} {
		h := runCORS(t, allowed, http.MethodGet, origin)
		if got := h.Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, DELETE, OPTIONS" {
			t.Errorf("methods header missing for origin %q: %q", origin, got)
		}
		if got := h.Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization" {
			t.Errorf("headers header missing for origin %q: %q", origin, got)
		}
	}
}

func TestCORS_PreflightOptions_ShortCircuits204(t *testing.T) {
	called := false
	handler := CORS([]string{"https://dpharm.web.app"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://dpharm.web.app")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if called {
		t.Fatalf("OPTIONS should not invoke next handler")
	}
	if rr.Result().StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS preflight, got %d", rr.Result().StatusCode)
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"single", "https://a.example", []string{"https://a.example"}},
		{
			"multiple with spaces",
			"https://a.example , https://b.example,https://c.example",
			[]string{"https://a.example", "https://b.example", "https://c.example"},
		},
		{"blanks dropped", ",,https://a.example,,", []string{"https://a.example"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseAllowedOrigins(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParseAllowedOrigins(%q) = %v; want %v", c.in, got, c.want)
			}
		})
	}
}
