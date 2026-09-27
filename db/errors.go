package db

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// IsDuplicateKey reports whether err is a unique-index violation (E11000).
func IsDuplicateKey(err error) bool {
	var we mongo.WriteException
	if errors.As(err, &we) {
		for _, e := range we.WriteErrors {
			if e.Code == 11000 {
				return true
			}
		}
	}
	return false
}
