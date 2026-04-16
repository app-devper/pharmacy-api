package middleware

import (
	"context"
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

func RequireAuth(secretKey string) func(http.Handler) http.Handler {
	jwtKey := []byte(secretKey)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(authHeader, "Bearer ", 2)
			if len(parts) < 2 || parts[1] == "" {
				http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
				return
			}

			claims := &AccessClaims{}
			tkn, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (interface{}, error) {
				return jwtKey, nil
			})
			if err != nil || tkn == nil || !tkn.Valid || claims.ID == "" {
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
