package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/priyanshu-s-rana/kv_store/config"
	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/models"
	"github.com/priyanshu-s-rana/kv_store/persistence"
	"github.com/priyanshu-s-rana/kv_store/server"
	"github.com/priyanshu-s-rana/kv_store/store"
	"github.com/priyanshu-s-rana/kv_store/utils"
)

// gracefulShutdown blocks until SIGINT or SIGTERM is received, then cancels the
// context, flushes a final snapshot to disk, and exits cleanly.
func gracefulShutdown(persist *persistence.Persistence) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[main] shutting down gracefully...")

	persist.Close()

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
	CONFIG := config.CONFIG

	host := utils.ResolveStringFallbacks(*hostFlag, CONFIG.Server.Host, "localhost")
	port := utils.ResolveStringFallbacks(*portFlag, CONFIG.Server.Port, "5040")
	addr := host + ":" + port

	memorySize := CONFIG.Memory.MaxSize

	cmdChan := make(chan models.Command)

	ctx, cancel := context.WithCancel(context.Background())
	log.Printf("[main] CONFIG: %+v", CONFIG)
	persist, err := persistence.New(
		ctx,
		cancel,
		cmdChan,
		[2]*persistence.AOFConfig{
			{
				FilePath:   CONFIG.Persistence.Journal.Path1,
				SyncPolicy: utils.ResolveStringFallbacks(CONFIG.Persistence.Journal.Policy, constants.SyncEverySec),
			},
			{
				FilePath:   CONFIG.Persistence.Journal.Path2,
				SyncPolicy: utils.ResolveStringFallbacks(CONFIG.Persistence.Journal.Policy, constants.SyncEverySec),
			},
		},
		&persistence.SnapshotConfig{
			FilePath: CONFIG.Persistence.Snapshot.Path,
			Interval: CONFIG.Persistence.Snapshot.Interval,
		},
	)
	if err != nil {
		log.Fatalf("[main] error initializing persistence: %v.", err)
	}

	store := store.New(memorySize, cmdChan, persist)
	store.Start()

	if err := persist.Recovery(); err != nil {
		log.Printf("[main] warning: failed to recover persistant data from disk: %v", err)
	}

	if err := persist.Start(); err != nil {
		log.Printf("[main] warning: failed to start persistence for the store: %v", err)
	}

	go gracefulShutdown(persist)

	server := server.New(addr, cmdChan, store)
	log.Printf("[main] KV Store starting server on port %s", port)
	if err = server.Start(); err != nil {
		log.Fatalf("[main] server error: %v", err)
	}
}
