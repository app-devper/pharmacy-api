package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Kinds of Abandonment (ADR-0009).
const (
	AbandonSale    = "sale"     // a queued sale the pharmacy will never record
	AbandonKyForms = "ky_forms" // KY forms of a recorded sale that were refused
)

// AbandonInput closes a queued sale or its refused KY forms that will not be
// recorded. Payload is what the client queued, kept verbatim for audit.
type AbandonInput struct {
	ClientRequestID string `json:"client_request_id"`
	Kind            string `json:"kind"`
	Payload         any    `json:"payload"`
	Reason          string `json:"reason"`
}

// Abandonment is the audit record of an abandoned sale or KY forms: one per
// kind and client request id.
type Abandonment struct {
	ID              bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	ClientRequestID string        `bson:"client_request_id" json:"client_request_id"`
	Kind            string        `bson:"kind"           json:"kind"`
	SaleID          string        `bson:"sale_id,omitempty" json:"sale_id,omitempty"`
	BillNo          string        `bson:"bill_no,omitempty" json:"bill_no,omitempty"`
	Payload         string        `bson:"payload"        json:"payload"` // JSON as the client queued it
	Reason          string        `bson:"reason"         json:"reason"`
	By              string        `bson:"by"             json:"by"`
	At              time.Time     `bson:"at"             json:"at"`
}
