// Package refusal is how a domain command (sales, inventory) says a request
// cannot succeed as sent. HTTP handlers map the Kind to a status; any other
// error is unexpected.
package refusal

// Kind classifies a refusal so a caller can choose a status.
type Kind int

const (
	// Invalid: the request cannot succeed as sent (400).
	Invalid Kind = iota + 1
	// NotFound: the referenced record does not exist (404).
	NotFound
	// Conflict: the request conflicts with recorded state (409).
	Conflict
)

// Error is a refusal the caller can show.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func Invalidf(msg string) error  { return &Error{Kind: Invalid, Msg: msg} }
func NotFoundf(msg string) error { return &Error{Kind: NotFound, Msg: msg} }
func Conflictf(msg string) error { return &Error{Kind: Conflict, Msg: msg} }
