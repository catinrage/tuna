package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tuna/internal/app/entry"
	"tuna/internal/config"
)

func main() {
	configPath := flag.String("config", "config.entry.json", "path to entry config file")
	flag.Parse()

	logger := log.New(os.Stdout, "tuna-entry ", log.LstdFlags|log.Lmicroseconds|log.LUTC)

	cfg, err := config.LoadEntry(*configPath)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := entry.New(cfg, logger)
	if err := server.Run(ctx); err != nil {
		logger.Fatalf("run entry server: %v", err)
	}
}
