package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/models"
)

func settingsTenant(t *testing.T) *MongoDB {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	prefix := fmt.Sprintf("db_test_%d", time.Now().UnixNano())
	mdb, err := NewManager(uri, prefix).ForClient("c1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if client, err := mongo.Connect(options.Client().ApplyURI(uri)); err == nil {
			ctx := context.Background()
			names, _ := client.ListDatabaseNames(ctx, bson.M{"name": bson.M{"$regex": "^" + prefix}})
			for _, n := range names {
				_ = client.Database(n).Drop(ctx)
			}
			_ = client.Disconnect(ctx)
		}
	})
	return mdb
}

func TestReadSettingsIsTheDefaultsUntilTheShopSaves(t *testing.T) {
	mdb := settingsTenant(t)
	got, err := mdb.ReadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := models.DefaultSettings().Effective(); got.Store.Name != want.Store.Name || got.Timezone != want.Timezone {
		t.Fatalf("got %+v, want the defaults", got)
	}
}

func TestReadSettingsFailsInsteadOfOfferingTheDefaults(t *testing.T) {
	mdb := settingsTenant(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mdb.ReadSettings(ctx); err == nil {
		t.Fatal("a failed read must be an error, or the settings form would save the defaults over the shop's own")
	}
	if got := mdb.StoreSettings(ctx); got.Location() == nil {
		t.Fatal("readers that carry on still get the defaults")
	}
}
