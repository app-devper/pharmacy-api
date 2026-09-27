package sales

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"pharmacy-pos/backend/db"
)

// A Commercial command (CONTEXT: Sales) runs at most once per client request
// id: the id is stored on the command's own record under a unique index,
// with a fingerprint of the request. Repeating the id with the same request
// returns the recorded outcome; with a different request it is refused.

// fingerprint is a stable digest of a request, without its request id.
func fingerprint(request any) string {
	b, _ := json.Marshal(request)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// recorded looks up the outcome recorded for the request id and the
// fingerprint stored with it; mongo.ErrNoDocuments when there is none.
type recorded[T any] func(ctx context.Context) (T, string, error)

// runOnce runs a command unless its request id already has an outcome. It
// reports whether the outcome was replayed rather than produced now.
func runOnce[T any](ctx context.Context, requestID, fp string, find recorded[T], run func() (T, error)) (T, bool, error) {
	if requestID == "" {
		out, err := run()
		return out, false, err
	}
	if out, ok, err := replay(ctx, fp, find); ok || err != nil {
		return out, ok, err
	}
	out, err := run()
	// Another attempt with the same id committed first.
	if err != nil && db.IsDuplicateKey(err) {
		if prior, ok, rerr := replay(ctx, fp, find); ok || rerr != nil {
			return prior, ok, rerr
		}
	}
	return out, false, err
}

func replay[T any](ctx context.Context, fp string, find recorded[T]) (T, bool, error) {
	var zero T
	out, storedFp, err := find(ctx)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	// Sales recorded before fingerprints existed carry none; accept them.
	if storedFp != "" && storedFp != fp {
		return zero, false, ErrRequestReused
	}
	return out, true, nil
}
