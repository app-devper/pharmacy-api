// Command seed writes the UM users and system the identity smoke test logs in
// with. It writes UM's collections directly, in UM's document shape, because
// UM has no bootstrap API for a first ADMIN.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// bcrypt of "smoke-password-1"; test-only credential.
const passwordHash = "$2a$04$j8Ufpplq4QGjIGxZePqvK.sEbkompIbu0Akv2yd543yyUcP9MJP6a"

// BobID is fixed so the script can delete bob through UM's API.
const BobID = "66aaaaaaaaaaaaaaaaaaaaaa"

func main() {
	uri, dbName := os.Getenv("MONGO_URI"), os.Getenv("UM_DB")
	if uri == "" || dbName == "" {
		log.Fatal("MONGO_URI and UM_DB are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(ctx)
	db := client.Database(dbName)
	if err := db.Drop(ctx); err != nil {
		log.Fatal(err)
	}

	bobID, _ := bson.ObjectIDFromHex(BobID)
	now := time.Now()
	users := []any{
		bson.M{"_id": bson.NewObjectID(), "username": "alice", "password": passwordHash,
			"clientId": "123", "role": "ADMIN", "status": "ACTIVE", "createdDate": now, "updatedDate": now},
		bson.M{"_id": bobID, "username": "bob", "password": passwordHash,
			"clientId": "123", "role": "USER", "status": "ACTIVE", "createdDate": now, "updatedDate": now},
	}
	if _, err := db.Collection("users").InsertMany(ctx, users); err != nil {
		log.Fatal(err)
	}
	if _, err := db.Collection("systems").InsertOne(ctx, bson.M{
		"_id": bson.NewObjectID(), "clientId": "123", "systemCode": "PHARMACY", "systemName": "Pharmacy", "createdDate": now,
	}); err != nil {
		log.Fatal(err)
	}
	log.Printf("seeded %s: alice (ADMIN), bob (USER), system PHARMACY for client 123", dbName)
}
