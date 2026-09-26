package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/app-devper/um-api/sessionclient"
	"github.com/golang-jwt/jwt/v5"
)

// Caching, system binding, and the Redis reader are tested in um-api's
// sessionclient module; these tests cover the pharmacy's route policy.

const testSecret = "test-secret"

type fakeStore struct {
	session sessionclient.Session
	err     error
	asked   string
}

func (f *fakeStore) Session(_ context.Context, sessionID string) (sessionclient.Session, error) {
	f.asked = sessionID
	return f.session, f.err
}

func signedToken(t *testing.T, sessionID, role, clientID string) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &AccessClaims{
		Role:     role,
		System:   "PHARMACY",
		ClientId: clientID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        sessionID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

// serve runs RequireAuth → live check → handler, returning the status and
// the role the handler saw.
func serve(t *testing.T, live *LiveIdentity, degradable bool) (int, string) {
	t.Helper()
	check := live.Require()
	if degradable {
		check = live.RequireOrDegrade()
	}
	var seenRole string
	handler := RequireAuth(testSecret, "PHARMACY")(check(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRole = GetRole(r.Context())
		w.WriteHeader(http.StatusOK)
	})))
	req := httptest.NewRequest(http.MethodGet, "/api/pharmacy/v1/sales", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken(t, "s1", "ADMIN", "123"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, seenRole
}

func liveWith(store *fakeStore) *LiveIdentity {
	return NewLiveIdentity(sessionclient.NewChecker(store))
}

func TestLiveSessionAuthorizesTokenClaims(t *testing.T) {
	store := &fakeStore{session: sessionclient.Session{UserId: "u1", System: "PHARMACY"}}
	code, role := serve(t, liveWith(store), false)
	if code != http.StatusOK || role != "ADMIN" || store.asked != "s1" {
		t.Fatalf("expected 200 as ADMIN for session s1, got %d %q asked=%q", code, role, store.asked)
	}
}

func TestRevokedSessionIsRefusedEverywhere(t *testing.T) {
	store := &fakeStore{err: sessionclient.ErrSessionRejected}
	for _, degradable := range []bool{false, true} {
		if code, _ := serve(t, liveWith(store), degradable); code != http.StatusUnauthorized {
			t.Fatalf("degradable=%v: expected 401, got %d", degradable, code)
		}
	}
}

func TestSessionFromAnotherSystemIsRefused(t *testing.T) {
	store := &fakeStore{session: sessionclient.Session{UserId: "u1", System: "POS"}}
	if code, _ := serve(t, liveWith(store), false); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a session issued to another system, got %d", code)
	}
}

func TestOutageBlocksProtectedRoutesButNotCatalog(t *testing.T) {
	store := &fakeStore{err: sessionclient.ErrUnavailable}
	if code, _ := serve(t, liveWith(store), false); code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on a protected route, got %d", code)
	}
	if code, _ := serve(t, liveWith(store), true); code != http.StatusOK {
		t.Fatalf("expected catalog read to degrade to 200, got %d", code)
	}
}

func TestDisabledCheckPassesThrough(t *testing.T) {
	for _, live := range []*LiveIdentity{NewLiveIdentity(nil), nil} {
		if code, _ := serve(t, live, false); code != http.StatusOK {
			t.Fatalf("expected passthrough, got %d", code)
		}
	}
}
