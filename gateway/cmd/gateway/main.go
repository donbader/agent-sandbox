// Gateway is a transparent proxy that runs inside the agent container.
// It intercepts all outbound traffic via iptables and either passes it through
// or applies credential injection via RequestHandlers.
package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/donbader/agent-sandbox/gateway/internal/ca"
	"github.com/donbader/agent-sandbox/gateway/internal/dns"
	"github.com/donbader/agent-sandbox/gateway/internal/mitm"
	"github.com/donbader/agent-sandbox/gateway/internal/proxy"
	"github.com/donbader/agent-sandbox/gateway/internal/redact"
)

const (
	// sharedCertPath is where the CA cert is written for the agent container (shared volume).
	sharedCertPath = "/shared/certs/ca.crt"
	// privateKeyPath is where the CA key is stored (gateway-internal, not shared).
	privateKeyPath = "/etc/gateway/private/ca.key"
)

func main() {
	// Setup structured logger
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	if os.Getenv("LOG_LEVEL") == "debug" {
		level.Set(slog.LevelDebug)
	}

	configPath := "/etc/gateway/config.yaml"
	if p := os.Getenv("GATEWAY_CONFIG"); p != "" {
		configPath = p
	}

	cfg, err := proxy.LoadConfig(configPath)
	if err != nil {
		// Minimal logger for startup errors (before secrets are known).
		slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	// Collect secret values from rewriter env vars for value-based redaction.
	secrets := collectSecrets(cfg.Rewriters)

	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Key-based redaction as a first layer (catches explicitly named attrs).
			if a.Key == "token" || a.Key == "authorization" || a.Key == "api_key" {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	})
	logger := slog.New(redact.NewHandler(jsonHandler, secrets))
	slog.SetDefault(logger)

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

	// Generate CA and register MITM handler if MITM domains are configured
	if len(cfg.MITMDomains) > 0 {
		slog.Info("generating CA keypair for MITM")
		caCert, err := ca.GenerateAndStore(sharedCertPath, privateKeyPath)
		if err != nil {
			slog.Error("generate CA", "error", err)
			os.Exit(1)
		}
		slog.Info("CA certificate written", "cert", sharedCertPath, "key", privateKeyPath)

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

	// Start port forwarders
	for _, pf := range cfg.PortForwards {
		fwd := proxy.NewForwarder(pf.Listen, pf.Target)
		go func() {
			if err := fwd.ListenAndServe(); err != nil {
				slog.Error("port forward error", "listen", pf.Listen, "target", pf.Target, "error", err)
			}
		}()
	}

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

// collectSecrets reads the raw secret values from environment variables referenced
// by rewriter configs. These values are used for value-based log redaction.
func collectSecrets(cfgs []proxy.RewriterConfig) []string {
	var secrets []string
	for _, rc := range cfgs {
		switch rc.Type {
		case "telegram-url":
			if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
				secrets = append(secrets, v)
			}
		case "auth-header":
			if rc.EnvVar != "" {
				if v := os.Getenv(rc.EnvVar); v != "" {
					secrets = append(secrets, v)
				}
			}
		}
	}
	return secrets
}
