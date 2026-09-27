// Package sales owns the Sales context's commercial commands: confirming a
// Sale, a Return against it, and a void (pharmacy ADR-0002, ADR-0005). Each
// runs in one transaction with its stock and customer-spend effects. HTTP
// handlers only decode requests and render outcomes.
package sales

import "errors"

// Kind classifies a refusal so a caller can choose a status.
type Kind int

const (
	// Invalid: the request cannot succeed as sent (400).
	Invalid Kind = iota + 1
	// NotFound: the referenced sale does not exist (404).
	NotFound
	// Conflict: the request conflicts with recorded state (409).
	Conflict
)

// Error is a refusal the caller can show. Any other error is unexpected.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func invalid(msg string) error  { return &Error{Kind: Invalid, Msg: msg} }
func notFound(msg string) error { return &Error{Kind: NotFound, Msg: msg} }
func conflict(msg string) error { return &Error{Kind: Conflict, Msg: msg} }

// ErrRequestReused: a client request id already confirmed a different
// command. The original is kept; the new one is refused.
var ErrRequestReused = &Error{Kind: Conflict, Msg: "client_request_id was already used for a different request"}

var errAlreadyVoided = errors.New("sale already voided")
