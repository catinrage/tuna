package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tuna/internal/app/exit"
	"tuna/internal/config"
)

func main() {
	configPath := flag.String("config", "config.exit.json", "path to exit config file")
	flag.Parse()

	logger := log.New(os.Stdout, "tuna-exit ", log.LstdFlags|log.Lmicroseconds|log.LUTC)

	cfg, err := config.LoadExit(*configPath)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	worker := exit.New(cfg, logger)
	if err := worker.Run(ctx); err != nil {
		logger.Fatalf("run exit worker: %v", err)
	}
}
