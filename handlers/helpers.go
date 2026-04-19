package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// bangkokTZ is the fallback location used when the tenant hasn't configured a
// timezone in Settings (or the configured value fails to load). The app
// originated in Thailand so Asia/Bangkok is the sensible default.
var bangkokTZ = func() *time.Location {
	if loc, err := time.LoadLocation(models.DefaultTimezone); err == nil {
		return loc
	}
	return time.FixedZone("Asia/Bangkok", 7*60*60)
}()

// loadTimezone returns the tenant's configured Settings.Timezone as a
// *time.Location. Falls back to `bangkokTZ` when the settings document is
// missing, the field is blank, or the configured IANA name can't be loaded
// (e.g. tzdata missing on the host). Never returns nil.
func loadTimezone(ctx context.Context, mdb *db.MongoDB) *time.Location {
	var s models.Settings
	if err := mdb.Settings().FindOne(ctx, bson.M{"key": settingsKey}).Decode(&s); err != nil {
		return bangkokTZ
	}
	if s.Timezone == "" {
		return bangkokTZ
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return bangkokTZ
	}
	return loc
}

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
