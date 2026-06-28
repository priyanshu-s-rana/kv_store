package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/priyanshu-s-rana/kv_store/config"
	"github.com/priyanshu-s-rana/kv_store/server"
	"github.com/priyanshu-s-rana/kv_store/store"
	"github.com/priyanshu-s-rana/kv_store/utils"
)

// gracefulShutdown blocks until SIGINT or SIGTERM is received, then cancels the
// context, flushes a final snapshot to disk, and exits cleanly.
func gracefulShutdown(store *store.Store, cancel context.CancelFunc, snpStats *store.SnapshotStats) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[main] shutting down gracefully...")
	cancel()
	if err := store.SaveToDisk(config.CONFIG.Snapshot.Path, snpStats); err != nil {
		log.Printf("[main] error saving snapshot to disk: %v", err)
	} else {
		log.Println("[main] final snapshot saved successfully")
	}
	log.Println("[main] shutdown complete")
	os.Exit(0)
}

func main() {
	envFlag := flag.String("env", "", "server env, tells which config to use")
	hostFlag := flag.String("h", "", "server host, overrides config.yaml")
	portFlag := flag.String("p", "", "server port, overrides config.yaml")
	flag.Parse()
	env := utils.ResolveEnv(*envFlag, os.Getenv("APP_ENV"))

	config.SetConfig(env)

	host := utils.ResolveStringFallbacks(*hostFlag, config.CONFIG.Server.Host, "localhost")
	port := utils.ResolveStringFallbacks(*portFlag, config.CONFIG.Server.Port, "5040")
	addr := host + ":" + port

	snapshotPath := config.CONFIG.Snapshot.Path
	snapshotInterval := config.CONFIG.Snapshot.Interval
	memorySize := config.CONFIG.Memory.MaxSize

	snapshotStats := &store.SnapshotStats{}
	store := store.New(memorySize)
	if err := store.LoadFromDisk(snapshotPath); err != nil {
		log.Printf("[main] warning: failed to load snapshot from disk: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if snapshotInterval > 0 {
		store.StartSnapshotting(ctx, snapshotPath, snapshotInterval, snapshotStats)
	}

	go gracefulShutdown(store, cancel, snapshotStats)

	server := server.New(addr, store)
	log.Printf("[main] KV Store starting server on port %s", port)
	if err := server.Start(); err != nil {
		log.Fatalf("[main] server error: %v", err)
	}
}
