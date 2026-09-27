// Package sales owns the Sales context's commercial commands: confirming a
// Sale, a Return against it, and a void (pharmacy ADR-0002, ADR-0005). Each
// runs in one transaction with its stock and customer-spend effects. HTTP
// handlers only decode requests and render outcomes.
package sales

import (
	"errors"

	"pharmacy-pos/backend/refusal"
)

// Refusals are shared with the inventory module (package refusal).
type (
	Kind  = refusal.Kind
	Error = refusal.Error
)

const (
	Invalid  = refusal.Invalid
	NotFound = refusal.NotFound
	Conflict = refusal.Conflict
)

func invalid(msg string) error  { return refusal.Invalidf(msg) }
func notFound(msg string) error { return refusal.NotFoundf(msg) }
func conflict(msg string) error { return refusal.Conflictf(msg) }

// ErrRequestReused: a client request id already confirmed a different
// command. The original is kept; the new one is refused.
var ErrRequestReused = &Error{Kind: Conflict, Msg: "client_request_id was already used for a different request"}

var errAlreadyVoided = errors.New("sale already voided")
