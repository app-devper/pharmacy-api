package db

import (
	"context"
	"errors"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"pharmacy-pos/backend/models"
)

// SettingsKey is the key of the tenant's single settings document.
const SettingsKey = "singleton"

// ReadSettings is the tenant's settings as the shop runs on them
// (models.Settings.Effective): the built-in defaults when the shop has never
// saved any. A failed read is an error, not "no settings", so a caller that
// shows settings for editing never offers the defaults in place of the
// shop's own and lets them be saved over it.
func (m *MongoDB) ReadSettings(ctx context.Context) (models.Settings, error) {
	var s models.Settings
	err := m.Settings().FindOne(ctx, bson.M{"key": SettingsKey}).Decode(&s)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.DefaultSettings().Effective(), nil
	}
	if err != nil {
		return models.Settings{}, err
	}
	return s.Effective(), nil
}

// StoreSettings is ReadSettings for a reader that must carry on (a sale's
// timezone, a report window): when the read fails it logs and runs on the
// built-in defaults. Every such reader goes through here, so there is one
// fallback rule.
func (m *MongoDB) StoreSettings(ctx context.Context) models.Settings {
	s, err := m.ReadSettings(ctx)
	if err != nil {
		log.Printf("settings: read failed, running on defaults: %v", err)
		return models.DefaultSettings().Effective()
	}
	return s
}

// Timezone is the tenant's timezone (StoreSettings().Location()).
func (m *MongoDB) Timezone(ctx context.Context) *time.Location {
	return m.StoreSettings(ctx).Location()
}
