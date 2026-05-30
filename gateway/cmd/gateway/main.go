// Gateway is a transparent proxy that runs inside the agent container.
// It intercepts all outbound traffic via iptables and either passes it through
// or applies credential injection via RequestHandlers.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/donbader/agent-sandbox/gateway/internal/dns"
	"github.com/donbader/agent-sandbox/gateway/internal/proxy"
)

func main() {
	configPath := "/etc/gateway/config.yaml"
	if p := os.Getenv("GATEWAY_CONFIG"); p != "" {
		configPath = p
	}

	cfg, err := proxy.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("gateway: load config: %v", err)
	}

	// Start DNS resolver
	dnsServer := dns.NewServer(cfg.DNSListen)
	go func() {
		if err := dnsServer.ListenAndServe(); err != nil {
			log.Fatalf("gateway: dns: %v", err)
		}
	}()
	log.Printf("gateway: dns listening on %s", cfg.DNSListen)

	// Start TCP proxy
	p := proxy.New(cfg)
	go func() {
		if err := p.ListenAndServe(); err != nil {
			log.Fatalf("gateway: proxy: %v", err)
		}
	}()
	log.Printf("gateway: proxy listening on %s", cfg.Listen)

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	log.Println("gateway: shutting down")
}
