package handlers

import (
	"context"
	"net/http"
	"time"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
)

// serve runs fn against the caller's tenant database under timeout and
// writes its answer: an unknown tenant is 403, a refusal maps to its status
// (writeCommandError), any other error is 500, and a result is 200 JSON.
// It is the one place a handler's tenant, deadline and error rendering are
// decided, so every route answers the same way.
func serve(dbm *db.Manager, w http.ResponseWriter, r *http.Request, timeout time.Duration, fn func(ctx context.Context, mdb *db.MongoDB) (any, error)) {
	mdb, err := dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	out, err := fn(ctx, mdb)
	render(w, out, err)
}

// render writes a command's or a read's outcome.
func render(w http.ResponseWriter, out any, err error) {
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, out)
}
