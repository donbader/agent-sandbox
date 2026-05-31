// Gateway is a transparent proxy that runs inside the agent container.
// It intercepts all outbound traffic via iptables and either passes it through
// or applies credential injection via RequestHandlers.
package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/donbader/agent-sandbox/gateway/internal/dns"
	"github.com/donbader/agent-sandbox/gateway/internal/mitm"
	"github.com/donbader/agent-sandbox/gateway/internal/proxy"
)

func main() {
	// Setup structured logger
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	if os.Getenv("LOG_LEVEL") == "debug" {
		level.Set(slog.LevelDebug)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == "token" || a.Key == "authorization" || a.Key == "api_key" {
				return slog.String(a.Key, "[redacted]")
			}
			return a
		},
	}))
	slog.SetDefault(logger)

	configPath := "/etc/gateway/config.yaml"
	if p := os.Getenv("GATEWAY_CONFIG"); p != "" {
		configPath = p
	}

	cfg, err := proxy.LoadConfig(configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	// Start DNS resolver
	dnsServer := dns.NewServer(cfg.DNSListen)
	go func() {
		if err := dnsServer.ListenAndServe(); err != nil {
			slog.Error("dns server error", "error", err)
			os.Exit(1)
		}
	}()
	slog.Info("dns listening", "addr", cfg.DNSListen)

	// Start TCP proxy
	p := proxy.New(cfg)

	// Register MITM handler if configured
	if len(cfg.MITMDomains) > 0 && cfg.CACertPath != "" && cfg.CAKeyPath != "" {
		caCert, err := mitm.LoadCA(cfg.CACertPath, cfg.CAKeyPath)
		if err != nil {
			slog.Error("load CA", "error", err)
			os.Exit(1)
		}

		// Build rewriters from config
		rewriters := buildRewriters(cfg.Rewriters)

		handler := mitm.NewHandler(cfg.MITMDomains, caCert, rewriters)
		p.RegisterHandler(handler)
		slog.Info("mitm enabled", "domains", cfg.MITMDomains)
	}

	go func() {
		if err := p.ListenAndServe(); err != nil {
			slog.Error("proxy error", "error", err)
			os.Exit(1)
		}
	}()
	slog.Info("proxy listening", "addr", cfg.Listen)

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	slog.Info("shutting down")
}

// buildRewriters instantiates rewriters from the gateway config.
// Each entry in cfgs maps to a specific rewriter type.
func buildRewriters(cfgs []proxy.RewriterConfig) []mitm.Rewriter {
	var rewriters []mitm.Rewriter
	for _, rc := range cfgs {
		switch rc.Type {
		case "telegram-url":
			rw, err := mitm.NewTelegramRewriter()
			if err != nil {
				slog.Error("telegram rewriter disabled", "error", err)
				continue
			}
			rewriters = append(rewriters, rw)
			slog.Info("telegram token rewriter enabled")
		case "auth-header":
			rw, err := mitm.NewAuthHeaderRewriter(rc.Domains, rc.Header, rc.ValueFormat, rc.EnvVar)
			if err != nil {
				slog.Error("auth-header rewriter disabled", "domains", rc.Domains, "header", rc.Header, "error", err)
				continue
			}
			rewriters = append(rewriters, rw)
			slog.Info("auth-header rewriter enabled", "domains", rc.Domains, "header", rc.Header)
		default:
			slog.Warn("unknown rewriter type", "type", rc.Type)
		}
	}
	return rewriters
}
