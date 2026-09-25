package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret"

type fakeSessions struct {
	identity Identity
	err      error
	calls    int
	asked    string
}

func (f *fakeSessions) Verify(_ context.Context, sessionID string) (Identity, error) {
	f.calls++
	f.asked = sessionID
	return f.identity, f.err
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

type liveHarness struct {
	live     *LiveIdentity
	sessions *fakeSessions
	clock    time.Time
}

func newLiveHarness() *liveHarness {
	h := &liveHarness{
		sessions: &fakeSessions{identity: Identity{UserId: "u1", System: "PHARMACY"}},
		clock:    time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
	}
	h.live = NewLiveIdentity(h.sessions)
	h.live.now = func() time.Time { return h.clock }
	return h
}

// serve runs RequireAuth → live check → handler, returning the status and
// the role the handler saw.
func (h *liveHarness) serve(t *testing.T, token string, degradable bool) (int, string) {
	t.Helper()
	check := h.live.Require()
	if degradable {
		check = h.live.RequireOrDegrade()
	}
	var seenRole string
	handler := RequireAuth(testSecret, "PHARMACY")(check(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRole = GetRole(r.Context())
		w.WriteHeader(http.StatusOK)
	})))
	req := httptest.NewRequest(http.MethodGet, "/api/pharmacy/v1/sales", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, seenRole
}

func TestLiveSessionAuthorizesTokenClaims(t *testing.T) {
	h := newLiveHarness()

	code, role := h.serve(t, signedToken(t, "s1", "ADMIN", "123"), false)

	if code != http.StatusOK || role != "ADMIN" {
		t.Fatalf("expected 200 with token role ADMIN, got %d role=%q", code, role)
	}
	if h.sessions.asked != "s1" {
		t.Fatalf("expected lookup of the token's session, got %q", h.sessions.asked)
	}
}

func TestLiveIdentityCachesForAtMost30Seconds(t *testing.T) {
	h := newLiveHarness()
	token := signedToken(t, "s1", "ADMIN", "123")

	h.serve(t, token, false)
	h.clock = h.clock.Add(IdentityTTL - time.Second)
	h.serve(t, token, false)
	if h.sessions.calls != 1 {
		t.Fatalf("expected cached answer within TTL, got %d lookups", h.sessions.calls)
	}

	h.clock = h.clock.Add(time.Second)
	h.serve(t, token, false)
	if h.sessions.calls != 2 {
		t.Fatalf("expected a new lookup after %v, got %d", IdentityTTL, h.sessions.calls)
	}
}

func TestLiveIdentityRevocationTakesEffectAfterCacheExpires(t *testing.T) {
	h := newLiveHarness()
	token := signedToken(t, "s1", "ADMIN", "123")
	h.serve(t, token, false)

	h.sessions.err = ErrSessionRejected
	h.clock = h.clock.Add(IdentityTTL)

	if code, _ := h.serve(t, token, false); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 once the session is gone, got %d", code)
	}
	if code, _ := h.serve(t, token, true); code != http.StatusUnauthorized {
		t.Fatalf("catalog reads must also stop for a revoked session, got %d", code)
	}
}

func TestLiveIdentityRejectsSessionFromAnotherSystem(t *testing.T) {
	h := newLiveHarness()
	h.sessions.identity.System = "POS"

	if code, _ := h.serve(t, signedToken(t, "s1", "ADMIN", "123"), false); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a session issued to another system, got %d", code)
	}
}

func TestLiveIdentityAcceptsLegacySessionWithoutSystem(t *testing.T) {
	h := newLiveHarness()
	h.sessions.identity.System = ""

	if code, _ := h.serve(t, signedToken(t, "s1", "ADMIN", "123"), false); code != http.StatusOK {
		t.Fatalf("expected legacy session without system to pass, got %d", code)
	}
}

func TestLiveIdentityOutageBlocksProtectedOperationsButNotCatalog(t *testing.T) {
	h := newLiveHarness()
	h.sessions.err = ErrUMUnavailable
	token := signedToken(t, "s1", "USER", "123")

	if code, _ := h.serve(t, token, false); code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for protected operation during outage, got %d", code)
	}
	code, role := h.serve(t, token, true)
	if code != http.StatusOK || role != "USER" {
		t.Fatalf("expected catalog read under signed token, got %d role=%q", code, role)
	}
}

func TestLiveIdentityServesFromCacheDuringShortOutage(t *testing.T) {
	h := newLiveHarness()
	token := signedToken(t, "s1", "USER", "123")
	h.serve(t, token, false)

	h.sessions.err = ErrUMUnavailable
	h.clock = h.clock.Add(IdentityTTL / 2)
	if code, _ := h.serve(t, token, false); code != http.StatusOK {
		t.Fatalf("expected cached answer to cover a short outage, got %d", code)
	}

	h.clock = h.clock.Add(IdentityTTL)
	if code, _ := h.serve(t, token, false); code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 once the cached answer expires, got %d", code)
	}
}

func TestLiveIdentityDisabledWithoutVerifier(t *testing.T) {
	h := newLiveHarness()
	h.live = NewLiveIdentity(nil)
	code, role := h.serve(t, signedToken(t, "s1", "USER", "123"), false)
	if code != http.StatusOK || role != "USER" {
		t.Fatalf("expected passthrough, got %d role=%q", code, role)
	}
}

// The key and JSON shape mirror um-api's session storage contract (UM ADR-0003).
func TestRedisVerifierReadsUMSessionKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb, err := NewUMRedisClient(mr.Addr())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer rdb.Close()
	v := NewRedisVerifier(rdb)
	ctx := context.Background()

	mr.Set("session:s1", `{"userId":"u1","createdAt":"2026-09-25T10:00:00Z","system":"PHARMACY"}`)
	id, err := v.Verify(ctx, "s1")
	if err != nil || id != (Identity{UserId: "u1", System: "PHARMACY"}) {
		t.Fatalf("unexpected identity %+v err=%v", id, err)
	}

	if _, err := v.Verify(ctx, "missing"); !errors.Is(err, ErrSessionRejected) {
		t.Fatalf("missing session: expected ErrSessionRejected, got %v", err)
	}

	mr.Set("session:bad", `not json`)
	if _, err := v.Verify(ctx, "bad"); !errors.Is(err, ErrUMUnavailable) {
		t.Fatalf("unreadable session: expected ErrUMUnavailable, got %v", err)
	}

	mr.SetError("LOADING")
	if _, err := v.Verify(ctx, "s1"); !errors.Is(err, ErrUMUnavailable) {
		t.Fatalf("redis error: expected ErrUMUnavailable, got %v", err)
	}
	mr.SetError("")

	mr.Close()
	if _, err := v.Verify(ctx, "s1"); !errors.Is(err, ErrUMUnavailable) {
		t.Fatalf("unreachable redis: expected ErrUMUnavailable, got %v", err)
	}
}

func TestNewUMRedisClientAcceptsURL(t *testing.T) {
	rdb, err := NewUMRedisClient("redis://:secret@10.0.0.5:6380/2")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	defer rdb.Close()
	opts := rdb.Options()
	if opts.Addr != "10.0.0.5:6380" || opts.Password != "secret" || opts.DB != 2 {
		t.Fatalf("unexpected options addr=%s db=%d", opts.Addr, opts.DB)
	}
}
