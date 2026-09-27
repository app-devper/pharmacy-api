package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/app-devper/um-api/sessionclient"

	"pharmacy-pos/backend/config"
	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/handlers"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/routes"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	manager := db.NewManager(cfg.MongoURI, cfg.DBPrefix)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Bootstrap the default client "000" with indexes + seed data.
	// Fail fast so we don't start serving with a broken default tenant.
	if err := manager.CreateIndexesForClient(ctx, "000"); err != nil {
		log.Fatalf("bootstrap indexes for default tenant: %v", err)
	}
	if err := manager.SeedForClient(ctx, "000"); err != nil {
		log.Fatalf("bootstrap seed for default tenant: %v", err)
	}

	dh := handlers.NewDrugHandler(manager)
	lh := handlers.NewDrugLotHandler(manager)
	ch := handlers.NewCustomerHandler(manager)
	sh := handlers.NewSaleHandler(manager)
	rh := handlers.NewReportHandler(manager)
	kh := handlers.NewKyHandler(manager)
	eh := handlers.NewExportHandler(manager)
	ih := handlers.NewImportHandler(manager)
	suph := handlers.NewSupplierHandler(manager)
	ah := handlers.NewStockAdjustmentHandler(manager)
	sch := handlers.NewStockCountHandler(manager)
	reth := handlers.NewReturnHandler(manager)
	mvh := handlers.NewMovementsHandler(manager)
	seth := handlers.NewSettingsHandler(manager)
	labh := handlers.NewLabelHandler()

	// Every request is verified against UM's live session (um-api ADR-0005);
	// without UM_REDIS_HOST the API refuses to start.
	umSessions, err := sessionclient.RedisStoreFor(cfg.UMRedisHost)
	if err != nil {
		log.Fatalf("UM_REDIS_HOST: %v", err)
	}
	identity, err := sessionclient.NewVerifier(sessionclient.Config{
		SecretKey: cfg.SecretKey,
		System:    cfg.System,
		Store:     umSessions,
	})
	if err != nil {
		log.Fatalf("UM token verification: %v", err)
	}

	r := routes.Setup(
		dh, lh, ch, sh, rh, kh, eh, ih, suph, ah, sch, reth, mvh, seth, labh,
		mw.ParseAllowedOrigins(cfg.FrontendOrigin),
		cfg.GatewayHosts,
		identity,
	)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Server running on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
