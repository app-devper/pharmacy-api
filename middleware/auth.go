package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type ctxKey string

const (
	CtxSessionID ctxKey = "sessionId"
	CtxRole      ctxKey = "role"
	CtxSystem    ctxKey = "system"
	CtxClientID  ctxKey = "clientId"
)

type AccessClaims struct {
	Role     string `json:"role"`
	System   string `json:"system"`
	ClientId string `json:"clientId"`
	jwt.RegisteredClaims
}

func RequireAuth(secretKey, expectedSystem string) func(http.Handler) http.Handler {
	jwtKey := []byte(secretKey)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			token, ok := bearerToken(r)
			if !ok {
				http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
				return
			}

			claims := &AccessClaims{}
			tkn, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
				if token.Method == nil || token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
					return nil, errors.New("unexpected signing method")
				}
				return jwtKey, nil
			})
			if err != nil || tkn == nil || !tkn.Valid || claims.ID == "" || claims.ClientId == "" {
				http.Error(w, `{"error":"token invalid or expired"}`, http.StatusUnauthorized)
				return
			}
			if claims.ExpiresAt == nil {
				http.Error(w, `{"error":"token invalid or expired"}`, http.StatusUnauthorized)
				return
			}
			if claims.System != expectedSystem {
				http.Error(w, `{"error":"token invalid or expired"}`, http.StatusUnauthorized)
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, CtxSessionID, claims.ID)
			ctx = context.WithValue(ctx, CtxRole, claims.Role)
			ctx = context.WithValue(ctx, CtxSystem, claims.System)
			ctx = context.WithValue(ctx, CtxClientID, claims.ClientId)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken returns the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func GetRole(ctx context.Context) string {
	if v, ok := ctx.Value(CtxRole).(string); ok {
		return v
	}
	return ""
}

func GetClientID(ctx context.Context) string {
	if v, ok := ctx.Value(CtxClientID).(string); ok {
		return v
	}
	return ""
}
