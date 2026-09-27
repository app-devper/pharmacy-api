package db

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

// SettingsKey is the key of the tenant's single settings document.
const SettingsKey = "singleton"

// defaultLocation is used when the tenant has not configured a timezone (or
// the configured one cannot be loaded). The app originated in Thailand.
var defaultLocation = func() *time.Location {
	if loc, err := time.LoadLocation(models.DefaultTimezone); err == nil {
		return loc
	}
	return time.FixedZone("Asia/Bangkok", 7*60*60)
}()

// Timezone returns the tenant's configured Settings.Timezone, falling back to
// Asia/Bangkok when the settings document is missing, the field is blank, or
// the IANA name cannot be loaded.
func (m *MongoDB) Timezone(ctx context.Context) *time.Location {
	var s models.Settings
	if err := m.Settings().FindOne(ctx, bson.M{"key": SettingsKey}).Decode(&s); err != nil {
		return defaultLocation
	}
	if s.Timezone == "" {
		return defaultLocation
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return defaultLocation
	}
	return loc
}
