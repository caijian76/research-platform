package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"research-platform/backend/internal/api"
	"research-platform/backend/internal/auth"
	"research-platform/backend/internal/config"
	"research-platform/backend/internal/db"
	"research-platform/backend/internal/kube"
)

func main() {
	cfg := config.Load()
	log.Printf("research platform backend starting")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var database *db.DB
	var err error
	for i := 0; i < 45; i++ {
		database, err = db.Open(ctx, cfg.DatabaseURL)
		if err == nil {
			break
		}
		log.Printf("waiting for postgres: %v", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	if err := database.Seed(ctx, cfg.AdminUsername, cfg.AdminPassword, cfg.SeedTemplateImage); err != nil {
		log.Fatalf("seed database: %v", err)
	}
	log.Printf("database ready, platform admin user: %s", cfg.AdminUsername)

	kc, err := kube.New(cfg.StorageClass, cfg.PublicHost, cfg.UnitNSPrefix)
	if err != nil {
		log.Fatalf("init kubernetes client: %v", err)
	}
	am, err := auth.New(cfg.JWTSecret, cfg.SecretKey)
	if err != nil {
		log.Fatalf("init auth: %v", err)
	}

	server := api.New(database, kc, am, cfg)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Printf("bye")
}