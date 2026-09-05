// Command server runs the checkout and rewards HTTP API.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayush/checkout-rewards/internal/config"
	"github.com/ayush/checkout-rewards/internal/db"
	"github.com/ayush/checkout-rewards/internal/httpapi"
	"github.com/ayush/checkout-rewards/internal/repository"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	gormDB, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.Ping(pingCtx, gormDB); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	if err := db.Migrate(ctx, gormDB, cfg.MigrationsDir); err != nil {
		log.Fatalf("run migrations: %v", err)
	}
	log.Println("migrations applied")

	if !cfg.SkipSeed {
		if err := db.Seed(ctx, gormDB, cfg.SeedDir); err != nil {
			log.Fatalf("seed database: %v", err)
		}
		log.Println("seed data applied")
	}

	log.Printf("reward program: every %d successful orders unlocks one %d%% off coupon",
		cfg.RewardMilestoneN, cfg.RewardDiscountPercent)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      httpapi.New(repository.New(gormDB, cfg.RewardMilestoneN, cfg.RewardDiscountPercent)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}
