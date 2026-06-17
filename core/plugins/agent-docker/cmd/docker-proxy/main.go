package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	if os.Getenv("LOG_LEVEL") == "debug" {
		level.Set(slog.LevelDebug)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	cfg, err := loadConfigFromEnv()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	proxy, err := NewDockerProxy(cfg)
	if err != nil {
		slog.Error("create proxy", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:    ":2375",
		Handler: proxy,
	}

	go func() {
		slog.Info("docker proxy listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown, then cleanup
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	slog.Info("shutting down, cleaning up containers")
	proxy.Cleanup()
}
