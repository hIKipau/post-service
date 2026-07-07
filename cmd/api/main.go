package main

import (
	"context"
	"log"

	"post-service/internal/app"
	"post-service/internal/config"
	"post-service/internal/logger"
)

func main() {
	cfg, err := config.MustLoad()
	if err != nil {
		log.Fatal(err)
	}

	logger := logger.NewLogger(cfg.Env)

	err = app.Run(context.Background(), cfg, logger)
	if err != nil {
		log.Fatal(err)
	}

}
