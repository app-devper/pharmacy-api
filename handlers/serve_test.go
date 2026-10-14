package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"pharmacy-pos/backend/refusal"
)

func TestEveryRouteRendersAnOutcomeTheSameWay(t *testing.T) {
	cases := []struct {
		name string
		out  any
		err  error
		want int
	}{
		{"a result", map[string]int{"n": 1}, nil, http.StatusOK},
		{"an invalid request", nil, refusal.Invalidf("bad month"), http.StatusBadRequest},
		{"a missing record", nil, refusal.NotFoundf("no such sale"), http.StatusNotFound},
		{"a conflict", nil, refusal.Conflictf("day closed"), http.StatusConflict},
		{"anything else", nil, errors.New("mongo down"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			render(w, c.out, c.err)
			if w.Code != c.want {
				t.Fatalf("status %d, want %d (body %s)", w.Code, c.want, w.Body.String())
			}
		})
	}
}
