package db

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/models"
)

// SettingsKey is the key of the tenant's single settings document.
const SettingsKey = "singleton"

// StoreSettings is the tenant's settings as the shop runs on them
// (models.Settings.Effective): the built-in defaults when the document is
// missing or cannot be read. Every reader of settings goes through here, so
// there is one fallback rule.
func (m *MongoDB) StoreSettings(ctx context.Context) models.Settings {
	var s models.Settings
	if err := m.Settings().FindOne(ctx, bson.M{"key": SettingsKey}).Decode(&s); err != nil {
		return models.DefaultSettings().Effective()
	}
	return s.Effective()
}

// Timezone is the tenant's timezone (StoreSettings().Location()).
func (m *MongoDB) Timezone(ctx context.Context) *time.Location {
	return m.StoreSettings(ctx).Location()
}
