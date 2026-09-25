package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Live identity (ADR-0001, ADR-0004): a signed token proves who the caller was
// when UM issued it; UM's session in Redis says whether it still holds. UM
// revokes a user's sessions whenever their role, status, password, or account
// changes, so a live session vouches for the token's claims. Answers are
// cached per session for at most IdentityTTL, so a revocation in UM takes
// effect here within 60 seconds.

const IdentityTTL = 30 * time.Second

var (
	// ErrSessionRejected means the session is gone or belongs to another system.
	ErrSessionRejected = errors.New("session rejected")
	// ErrUMUnavailable means the session could not be looked up.
	ErrUMUnavailable = errors.New("UM session store unavailable")
)

// Identity is UM's stored session, as written by um-api at "session:<jti>".
type Identity struct {
	UserId string `json:"userId"`
	System string `json:"system"`
}

type IdentityVerifier interface {
	Verify(ctx context.Context, sessionID string) (Identity, error)
}

// RedisVerifier reads UM's session keys. It only reads; UM owns the keys.
type RedisVerifier struct {
	rdb *redis.Client
}

func NewRedisVerifier(rdb *redis.Client) *RedisVerifier {
	return &RedisVerifier{rdb: rdb}
}

// identityLookupTimeout bounds a session lookup, including dial and retry, so
// a Redis outage turns into a prompt 503 instead of a hung request.
const identityLookupTimeout = time.Second

// NewUMRedisClient connects to UM's Redis from a host:port or redis:// URL.
func NewUMRedisClient(host string) (*redis.Client, error) {
	opts := &redis.Options{Addr: host}
	if strings.Contains(host, "://") {
		parsed, err := redis.ParseURL(host)
		if err != nil {
			return nil, err
		}
		opts = parsed
	}
	// go-redis ignores context deadlines on sockets unless told otherwise and
	// retries with its own 3-second timeouts; keep a lookup within the bound.
	opts.ContextTimeoutEnabled = true
	opts.DialTimeout = identityLookupTimeout
	opts.ReadTimeout = identityLookupTimeout
	opts.WriteTimeout = identityLookupTimeout
	opts.MaxRetries = -1
	return redis.NewClient(opts), nil
}

func (v *RedisVerifier) Verify(ctx context.Context, sessionID string) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, identityLookupTimeout)
	defer cancel()
	raw, err := v.rdb.Get(ctx, "session:"+sessionID).Result()
	if errors.Is(err, redis.Nil) {
		return Identity{}, ErrSessionRejected
	}
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrUMUnavailable, err)
	}
	var id Identity
	if err := json.Unmarshal([]byte(raw), &id); err != nil || id.UserId == "" {
		return Identity{}, fmt.Errorf("%w: unreadable session", ErrUMUnavailable)
	}
	return id, nil
}

// LiveIdentity checks each request's session after RequireAuth has validated
// the signed token. A nil verifier disables the check.
type LiveIdentity struct {
	verifier IdentityVerifier
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]cachedIdentity
}

type cachedIdentity struct {
	identity  Identity
	checkedAt time.Time
}

func NewLiveIdentity(verifier IdentityVerifier) *LiveIdentity {
	return &LiveIdentity{verifier: verifier, now: time.Now, entries: map[string]cachedIdentity{}}
}

// Require protects writes and sensitive reads: without a current answer the
// request is refused.
func (l *LiveIdentity) Require() func(http.Handler) http.Handler {
	return l.middleware(false)
}

// RequireOrDegrade protects ordinary catalog reads: if the session store is
// unreachable and no current answer is cached, the request continues under
// the signed token.
func (l *LiveIdentity) RequireOrDegrade() func(http.Handler) http.Handler {
	return l.middleware(true)
}

func (l *LiveIdentity) middleware(degradable bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if l == nil || l.verifier == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := l.check(r.Context())
			switch {
			case err == nil:
				next.ServeHTTP(w, r)
			case errors.Is(err, ErrUMUnavailable) && degradable:
				log.Printf("identity: %v; serving catalog read under signed token", err)
				next.ServeHTTP(w, r)
			case errors.Is(err, ErrUMUnavailable):
				log.Printf("identity: %v", err)
				http.Error(w, `{"error":"identity service unavailable"}`, http.StatusServiceUnavailable)
			default:
				http.Error(w, `{"error":"session revoked or expired"}`, http.StatusUnauthorized)
			}
		})
	}
}

// check confirms the token's session is live in UM and was issued for the
// token's system.
func (l *LiveIdentity) check(ctx context.Context) error {
	sessionID, _ := ctx.Value(CtxSessionID).(string)
	system, _ := ctx.Value(CtxSystem).(string)

	now := l.now()
	l.mu.Lock()
	cached, ok := l.entries[sessionID]
	l.mu.Unlock()
	if ok && now.Sub(cached.checkedAt) < IdentityTTL {
		return nil
	}

	id, err := l.verifier.Verify(ctx, sessionID)
	if err == nil && id.System != "" && id.System != system {
		err = fmt.Errorf("%w: session was issued for system %q", ErrSessionRejected, id.System)
	}
	if err != nil {
		if !errors.Is(err, ErrUMUnavailable) {
			l.forget(sessionID)
		}
		return err
	}

	l.mu.Lock()
	l.entries[sessionID] = cachedIdentity{identity: id, checkedAt: now}
	if len(l.entries) > 10000 {
		for key, entry := range l.entries {
			if now.Sub(entry.checkedAt) >= IdentityTTL {
				delete(l.entries, key)
			}
		}
	}
	l.mu.Unlock()
	return nil
}

func (l *LiveIdentity) forget(sessionID string) {
	l.mu.Lock()
	delete(l.entries, sessionID)
	l.mu.Unlock()
}
