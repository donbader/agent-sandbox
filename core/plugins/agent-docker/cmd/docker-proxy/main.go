package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
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

	// Load gateway routing script (written by gateway at startup).
	// In compose mode, spawned containers need this for transparent proxy setup.
	if cfg.AllowCompose {
		if err := loadGatewayRouteScript(cfg); err != nil {
			slog.Error("load gateway route script", "error", err)
			os.Exit(1)
		}
		slog.Info("loaded gateway route script", "gateway_ip", cfg.GatewayIP)
	}

	proxy, err := NewDockerProxy(cfg)
	if err != nil {
		slog.Error("create proxy", "error", err)
		os.Exit(1)
	}

	// Discover the sandbox network ID by inspecting our own container.
	// This ensures spawned containers join the exact same network instance
	// the agent is on, regardless of subnet or duplicate network names.
	if err := proxy.DiscoverSandboxNetwork(); err != nil {
		slog.Error("discover sandbox network", "error", err)
		os.Exit(1)
	}

	// Initialize volume translator (discovers agent mounts, checks Docker version)
	if cfg.AllowCompose {
		proxy.volumes = proxy.NewVolumeTranslator()
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

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	slog.Info("shutting down")

	// Gracefully stop accepting new requests (5s timeout for in-flight)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		slog.Warn("server shutdown error", "error", err)
	}

	// Clean up spawned containers
	slog.Info("cleaning up containers")
	proxy.Cleanup()
}
