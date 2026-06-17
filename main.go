package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/priyanshu-s-rana/kv_store/config"
	"github.com/priyanshu-s-rana/kv_store/server"
	"github.com/priyanshu-s-rana/kv_store/store"
)

func gracefulShutdown(store *store.Store, cancel context.CancelFunc) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[main] shutting down gracefully...")
	cancel()
	if err := store.SaveToDisk(config.CONFIG.Snapshot.Path); err != nil {
		log.Printf("[main] error saving snapshot to disk: %v", err)
	} else {
		log.Println("[main] final snapshot saved successfully")
	}
	log.Println("[main] shutdown complete")
	os.Exit(0)
}

func main() {
	config.SetConfig()

	port := config.CONFIG.Server.Port
	snapshotPath := config.CONFIG.Snapshot.Path
	snapshotInterval := config.CONFIG.Snapshot.Interval

	store := store.New()

	if err := store.LoadFromDisk(snapshotPath); err != nil {
		log.Printf("[main] warning: failed to load snapshot from disk: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if snapshotInterval > 0 {
		store.StartSnapshotting(ctx, snapshotPath, snapshotInterval)
	}


	go gracefulShutdown(store, cancel)

	server := server.New(port, store)
	log.Printf("[main] KV Store starting server on port %s", port)
	if err := server.Start(); err != nil {
		log.Fatalf("[main] server error: %v", err)
	}
}
