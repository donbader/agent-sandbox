// Gateway is a transparent proxy that runs inside the agent container.
// It intercepts all outbound traffic via iptables and either passes it through
// or applies credential injection via middleware.
package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/donbader/agent-sandbox/core/gateway/internal/ca"
	"github.com/donbader/agent-sandbox/core/gateway/internal/dns"
	"github.com/donbader/agent-sandbox/core/gateway/internal/mitm"
	"github.com/donbader/agent-sandbox/core/gateway/internal/pluginloader"
	"github.com/donbader/agent-sandbox/core/gateway/internal/proxy"
	"github.com/donbader/agent-sandbox/core/gateway/internal/redact"
	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

const (
	// sharedCertPath is where the CA cert is written for the agent container (shared volume).
	sharedCertPath = "/shared/certs/ca.crt"
	// privateKeyPath is where the CA key is stored (persistent on shared volume, 0600).
	privateKeyPath = "/shared/certs/ca.key"
	// gatewayRouteScriptPath is the routing script written for sandbox containers.
	gatewayRouteScriptPath = "/shared/certs/gateway-route.sh"
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

	// Load TypeScript plugins (registers middleware + routes via SDK)
	pluginsConfigPath := "/etc/gateway/plugins.yaml"
	if p := os.Getenv("GATEWAY_PLUGINS_CONFIG"); p != "" {
		pluginsConfigPath = p
	}
	if err := pluginloader.LoadPluginsFromFile(pluginsConfigPath); err != nil {
		slog.Error("load plugins", "error", err)
		os.Exit(1)
	}

	// Register auth-header middleware from config
	for i, ah := range cfg.AuthHeaders {
		domain := ah.Domain
		header := ah.Header
		value := expandEnvVars(ah.Value)
		if value == "" {
			slog.Warn("auth-header skipped: env var not set", "domain", domain, "header", header)
			continue
		}
		name := fmt.Sprintf("auth-header:%s:%d", domain, i)
		gateway.RegisterSecret(value)
		gateway.RegisterMiddleware(name, func(ctx *gateway.MiddlewareContext) error {
			ctx.Request.Header.Set(header, value)
			return nil
		})
		gateway.BindDomains(name, []string{domain})
	}

	// Collect static secrets known at startup (from config auth_headers).
	// Dynamic secrets registered by TS plugins at request time are picked up
	// via WithSecretsFunc which reads the live registry on every log record.
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
	handler := redact.NewHandler(jsonHandler, secrets).WithSecretsFunc(gateway.Secrets)
	logger := slog.New(handler)
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
		caCert, err := ca.GenerateAndStore(sharedCertPath, privateKeyPath)
		if err != nil {
			slog.Error("generate CA", "error", err)
			os.Exit(1)
		}

		mitmHandler := mitm.NewHandler(cfg.MITMDomains, caCert)

		// Wire deny_paths checking from egress rules into MITM handler
		if len(cfg.EgressRules) > 0 {
			egressFilter := proxy.NewEgressFilter(cfg.EgressRules)
			mitmHandler.DenyPathChecker = func(host, method, path string) bool {
				decision := egressFilter.AllowHost(host)
				if decision.Rule != nil && len(decision.Rule.DenyPaths) > 0 {
					return !egressFilter.AllowPath(decision.Rule, method, path)
				}
				return false
			}
		}

		p.RegisterHandler(mitmHandler)
		slog.Info("mitm enabled", "domains", cfg.MITMDomains)
	}

	// Register HTTP proxy handler (for plain HTTP services)
	{
		egressFilter := proxy.NewEgressFilter(cfg.EgressRules)

		// Build HTTP services from egress rules with target specified
		httpServices := append([]proxy.HTTPService{}, cfg.HTTPServices...)
		for _, rule := range cfg.EgressRules {
			if rule.Target != "" && !rule.Deny {
				host, port, err := net.SplitHostPort(rule.Target)
				if err == nil {
					httpServices = append(httpServices, proxy.HTTPService{
						Host: host,
						Port: port,
					})
				}
			}
		}

		httpHandler := proxy.NewHTTPHandler(httpServices, egressFilter)
		p.RegisterHTTPHandler(httpHandler)
		if len(httpServices) > 0 {
			slog.Info("http proxy enabled", "services", httpServices)
		}
		if len(cfg.EgressRules) > 0 {
			slog.Info("egress rules loaded", "count", len(cfg.EgressRules))
		}
	}

	go func() {
		slog.Error("proxy error", "error", p.ListenAndServe())
		os.Exit(1)
	}()
	slog.Info("proxy listening", "addr", cfg.Listen)

	// Start port forwarders
	for _, pf := range cfg.PortForwards {
		fwd := proxy.NewForwarder(pf.Listen, pf.Target)
		go func() {
			slog.Error("port forward error", "listen", pf.Listen, "target", pf.Target, "error", fwd.ListenAndServe())
		}()
	}

	// Write routing script to shared volume for sandbox containers.
	// Skip gracefully if /shared/certs doesn't exist (e.g., CI smoke tests outside Docker).
	if _, err := os.Stat("/shared/certs"); err == nil {
		if err := writeGatewayRouteScript(); err != nil {
			slog.Error("write gateway route script", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Warn("shared certs volume not found, skipping route script", "path", "/shared/certs")
	}

	// Set up iptables PREROUTING to redirect forwarded traffic (port 443) to proxy (port 8443).
	// Sandbox containers route all traffic via this gateway; packets arrive with dest port 443
	// and need to be redirected to the local proxy listener on 8443.
	// Skip if iptables is not available (e.g., CI smoke tests outside Docker).
	if err := setupIptables(); err != nil {
		slog.Warn("iptables setup skipped", "error", err)
	}

	// Health + route handler endpoint
	healthAddr := ":8080"
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		// Serve plugin-registered routes (e.g. /plugins/mcp-oauth/callback)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			handler := gateway.MatchRoute(r.URL.Path)
			if handler != nil {
				handler(w, r)
				return
			}
			http.NotFound(w, r)
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

// getSandboxIP returns the gateway's IP on the sandbox network.
// It prefers the GATEWAY_SANDBOX_IP env var; otherwise it detects from interfaces.
func getSandboxIP() (string, error) {
	if ip := os.Getenv("GATEWAY_SANDBOX_IP"); ip != "" {
		return ip, nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no non-loopback IPv4 address found")
}

// writeGatewayRouteScript writes the routing script to the shared volume.
// Sandbox containers source this script to configure their default route and CA trust.
func writeGatewayRouteScript() error {
	ip, err := getSandboxIP()
	if err != nil {
		return fmt.Errorf("detect sandbox IP: %w", err)
	}

	script := `#!/bin/sh
# Gateway-authored routing for sandbox containers.
# Written by gateway at startup. IP is baked in.
# Containers only need: default route + CA trust + DNS.
# The gateway handles traffic interception via PREROUTING (no iptables needed here).
GATEWAY_IP="` + ip + `"

# Default route — send all traffic to the gateway.
# On internal:true networks there is no pre-existing default route.
ip route add default via "$GATEWAY_IP" 2>/dev/null || ip route replace default via "$GATEWAY_IP" 2>/dev/null || true

# CA certificate — enables HTTPS through gateway MITM.
if [ -f /shared/certs/ca.crt ]; then
    if ! grep -qF "$(sed -n '2p' /shared/certs/ca.crt)" /etc/ssl/certs/ca-certificates.crt 2>/dev/null; then
        cat /shared/certs/ca.crt >> /etc/ssl/certs/ca-certificates.crt 2>/dev/null || true
    fi
    export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
    export NODE_EXTRA_CA_CERTS=/shared/certs/ca.crt
    export NODE_USE_SYSTEM_CA=1
fi

# DNS — point at gateway's forwarder, keep Docker DNS as fallback.
if [ -w /etc/resolv.conf ]; then
    printf 'nameserver %s\nnameserver 127.0.0.11\n' "$GATEWAY_IP" > /etc/resolv.conf
fi
`

	if err := os.WriteFile(gatewayRouteScriptPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("write %s: %w", gatewayRouteScriptPath, err)
	}
	slog.Info("wrote gateway route script", "path", gatewayRouteScriptPath, "gateway_ip", ip)
	return nil
}

// setupIptables configures the gateway as a transparent proxy router.
// 1. Enable IP forwarding so routed packets are processed.
// 2. PREROUTING REDIRECT: forwarded tcp/443 → local proxy on 8443.
// 3. PREROUTING REDIRECT: forwarded tcp/80 → local HTTP handler on 8080.
func setupIptables() error {
	// Enable IP forwarding.
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644); err != nil {
		return fmt.Errorf("enable ip_forward: %w", err)
	}
	slog.Info("enabled ip forwarding")

	// HTTPS: redirect forwarded port 443 → proxy port 8443.
	cmd := exec.Command("iptables", "-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", "443", "-j", "REDIRECT", "--to-port", "8443")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables PREROUTING 443→8443: %w: %s", err, out)
	}
	slog.Info("iptables: PREROUTING tcp/443 → 8443")

	// HTTP: redirect forwarded port 80 → HTTP handler on 8080.
	cmd = exec.Command("iptables", "-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", "80", "-j", "REDIRECT", "--to-port", "8080")
	if out, err := cmd.CombinedOutput(); err != nil {
		// HTTP redirect is best-effort; not all gateways handle HTTP proxy.
		slog.Warn("iptables PREROUTING 80→8080 failed (non-fatal)", "error", fmt.Sprintf("%s: %s", err, out))
	} else {
		slog.Info("iptables: PREROUTING tcp/80 → 8080")
	}

	return nil
}

// expandEnvVars replaces all ${VAR} patterns in s with os.Getenv(VAR).
func expandEnvVars(s string) string {
	for {
		start := indexOf(s, "${")
		if start == -1 {
			return s
		}
		end := indexOf(s[start+2:], "}")
		if end == -1 {
			return s
		}
		varName := s[start+2 : start+2+end]
		envVal := os.Getenv(varName)
		s = s[:start] + envVal + s[start+2+end+1:]
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
