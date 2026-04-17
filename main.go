package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"pharmacy-pos/backend/config"
	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/handlers"
	"pharmacy-pos/backend/routes"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	database := db.Connect(cfg.MongoURI, cfg.DBName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	database.CreateIndexes(ctx)
	database.Seed(ctx)

	dh := handlers.NewDrugHandler(database)
	lh := handlers.NewDrugLotHandler(database)
	ch := handlers.NewCustomerHandler(database)
	sh := handlers.NewSaleHandler(database)
	rh := handlers.NewReportHandler(database)
	kh := handlers.NewKyHandler(database)
	eh := handlers.NewExportHandler(database)
	ih := handlers.NewImportHandler(database)
	suph := handlers.NewSupplierHandler(database)
	ah := handlers.NewStockAdjustmentHandler(database)
	reth := handlers.NewReturnHandler(database)
	mvh := handlers.NewMovementsHandler(database)

	r := routes.Setup(dh, lh, ch, sh, rh, kh, eh, ih, suph, ah, reth, mvh, cfg.SecretKey)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Server running on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
