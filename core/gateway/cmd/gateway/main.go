// Gateway is a transparent proxy that runs inside the agent container.
// It intercepts all outbound traffic via iptables and either passes it through
// or applies credential injection via middleware.
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
		slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	// Register built-in middleware from config (auth-header, oauth).
	// These use the same SDK registry as custom middleware.
	registerBuiltinMiddleware(cfg.Middlewares)

	// Collect secrets from all middleware for log redaction.
	secrets := gateway.Secrets()

	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
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

		handler := mitm.NewHandler(cfg.MITMDomains, caCert)
		p.RegisterHandler(handler)
		slog.Info("mitm enabled", "domains", cfg.MITMDomains)
	}

	// Register HTTP proxy handler (for plain HTTP services)
	{
		httpHandler := proxy.NewHTTPHandler(cfg.HTTPServices)
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

// registerBuiltinMiddleware instantiates built-in middleware from config.
func registerBuiltinMiddleware(cfgs []proxy.MiddlewareConfig) {
	for _, mc := range cfgs {
		if len(mc.Domains) == 0 {
			slog.Warn("middleware entry has no domains, skipping", "type", mc.Type)
			continue
		}
		switch mc.Type {
		case "auth-header":
			if err := mitm.RegisterAuthHeaderMiddleware(
				"auth-header:"+mc.Domains[0],
				mc.Domains, mc.Header, mc.ValueFormat, mc.EnvVar,
			); err != nil {
				slog.Error("auth-header middleware disabled", "domains", mc.Domains, "error", err)
				continue
			}
			slog.Info("auth-header middleware enabled", "domains", mc.Domains, "header", mc.Header)
		case "oauth":
			if err := mitm.RegisterOAuthMiddleware(
				"oauth:"+mc.Domains[0],
				mc.Domains, mc.TokenFile,
			); err != nil {
				slog.Error("oauth middleware disabled", "domains", mc.Domains, "error", err)
				continue
			}
			// Register initial token secrets for redaction.
			for _, s := range mitm.OAuthSecrets(mc.TokenFile) {
				gateway.RegisterSecret(s)
			}
			slog.Info("oauth middleware enabled", "domains", mc.Domains, "token_file", mc.TokenFile)
		default:
			slog.Warn("unknown middleware type", "type", mc.Type)
		}
	}
}

