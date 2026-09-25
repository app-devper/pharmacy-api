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

	var verifier mw.IdentityVerifier
	if cfg.UMRedisHost != "" {
		rdb, err := mw.NewUMRedisClient(cfg.UMRedisHost)
		if err != nil {
			log.Fatalf("UM_REDIS_HOST: %v", err)
		}
		defer rdb.Close()
		// Don't fail startup on a Redis outage: catalog reads can still be served.
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("WARNING: UM Redis is unreachable at startup: %v", err)
		}
		verifier = mw.NewRedisVerifier(rdb)
	} else {
		log.Printf("WARNING: UM_REDIS_HOST is not set; live UM session verification (ADR-0004) is disabled and revoked sessions stay valid until token expiry")
	}

	r := routes.Setup(
		dh, lh, ch, sh, rh, kh, eh, ih, suph, ah, sch, reth, mvh, seth, labh,
		cfg.SecretKey, cfg.System,
		mw.ParseAllowedOrigins(cfg.FrontendOrigin),
		cfg.GatewayHosts,
		mw.NewLiveIdentity(verifier),
	)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Server running on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
