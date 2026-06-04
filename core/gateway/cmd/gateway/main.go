// Gateway is a transparent proxy that runs inside the agent container.
// It intercepts all outbound traffic via iptables and either passes it through
// or applies credential injection via RequestHandlers.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/donbader/agent-sandbox/core/gateway/internal/ca"
	"github.com/donbader/agent-sandbox/core/gateway/internal/dns"
	"github.com/donbader/agent-sandbox/core/gateway/internal/mitm"
	"github.com/donbader/agent-sandbox/core/gateway/internal/proxy"
	"github.com/donbader/agent-sandbox/core/gateway/internal/redact"
	"github.com/donbader/agent-sandbox/core/sdk/gateway"

	// Custom middleware compilation target — user .go files are copied here at generate-time.
	_ "github.com/donbader/agent-sandbox/core/gateway/middlewares/custom"
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

	// Build rewriters early so we can collect secrets from them (e.g. OAuth tokens)
	// before constructing the redacting logger.
	rewriters := buildRewriters(cfg.Rewriters)

	// Collect secret values for value-based log redaction from two sources:
	// 1. Rewriters that implement SecretProvider (auth-header env vars, OAuth tokens).
	var secrets []string
	for _, rw := range rewriters {
		if sp, ok := rw.(mitm.SecretProvider); ok {
			secrets = append(secrets, sp.Secrets()...)
		}
	}
	// 2. Secrets declared by custom middleware via gateway.RegisterSecret().
	secrets = append(secrets, gateway.Secrets()...)

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

		handler := mitm.NewHandler(cfg.MITMDomains, caCert, rewriters)
		p.RegisterHandler(handler)
		slog.Info("mitm enabled", "domains", cfg.MITMDomains)
	}

	// Register HTTP proxy handler (for plain HTTP services with header injection)
	{
		var httpRewriters []proxy.HTTPRewriter
		for _, rw := range rewriters {
			httpRewriters = append(httpRewriters, rw)
		}
		httpHandler := proxy.NewHTTPHandler(cfg.HTTPServices, httpRewriters)
		p.RegisterHTTPHandler(httpHandler)
		if len(cfg.HTTPServices) > 0 {
			slog.Info("http proxy enabled", "services", cfg.HTTPServices)
		}
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

	// Health endpoint
	healthAddr := ":8080"
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		if err := http.ListenAndServe(healthAddr, mux); err != nil {
			slog.Error("health server error", "error", err)
		}
	}()
	slog.Info("health endpoint listening", "addr", healthAddr)

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
		case "auth-header":
			rw, err := mitm.NewAuthHeaderRewriter(rc.Domains, rc.Header, rc.ValueFormat, rc.EnvVar)
			if err != nil {
				slog.Error("auth-header rewriter disabled", "domains", rc.Domains, "header", rc.Header, "error", err)
				continue
			}
			rewriters = append(rewriters, rw)
			slog.Info("auth-header rewriter enabled", "domains", rc.Domains, "header", rc.Header)
		case "oauth":
			rw, err := mitm.NewOAuthRewriter(rc.Domains, rc.TokenFile)
			if err != nil {
				slog.Error("oauth rewriter disabled", "domains", rc.Domains, "error", err)
				continue
			}
			rewriters = append(rewriters, rw)
			slog.Info("oauth rewriter enabled", "domains", rc.Domains, "token_file", rc.TokenFile)
		default:
			slog.Warn("unknown rewriter type", "type", rc.Type)
		}
	}
	return rewriters
}

