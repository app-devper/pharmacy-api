package middleware

import (
	"errors"
	"log"
	"net/http"

	"github.com/app-devper/um-api/sessionclient"
)

// Live identity (ADR-0001, ADR-0004): a signed token proves who the caller was
// when UM issued it; UM's session store says whether it still holds. The
// lookup, its 30-second cache, and the system binding live in um-api's
// sessionclient module (um-api ADR-0004); this file only applies the
// pharmacy's per-route outage policy.

// LiveIdentity checks each request's session after RequireAuth has validated
// the signed token. A nil or disabled checker turns the check off.
type LiveIdentity struct {
	checker *sessionclient.Checker
}

func NewLiveIdentity(checker *sessionclient.Checker) *LiveIdentity {
	return &LiveIdentity{checker: checker}
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
		if l == nil || !l.checker.Enabled() {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			sessionID, _ := ctx.Value(CtxSessionID).(string)
			system, _ := ctx.Value(CtxSystem).(string)
			_, err := l.checker.Check(ctx, sessionID, system)
			switch {
			case err == nil:
				next.ServeHTTP(w, r)
			case errors.Is(err, sessionclient.ErrUnavailable) && degradable:
				log.Printf("identity: %v; serving catalog read under signed token", err)
				next.ServeHTTP(w, r)
			case errors.Is(err, sessionclient.ErrUnavailable):
				log.Printf("identity: %v", err)
				http.Error(w, `{"error":"identity service unavailable"}`, http.StatusServiceUnavailable)
			default:
				http.Error(w, `{"error":"session revoked or expired"}`, http.StatusUnauthorized)
			}
		})
	}
}
