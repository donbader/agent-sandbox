package proxy

import (
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds gateway configuration.
type Config struct {
	Listen       string        `yaml:"listen"`        // TCP listen address (e.g., ":8443")
	DNSListen    string        `yaml:"dns_listen"`    // DNS listen address (e.g., ":53")
	MITMDomains  []string      `yaml:"mitm_domains"`  // domains to MITM (terminate TLS)
	HTTPServices []HTTPService `yaml:"http_services"` // plain HTTP services to proxy
	PortForwards []PortForward `yaml:"port_forwards"` // TCP port forwards to agent container
}

// HTTPService describes a plain HTTP service the gateway should proxy.
type HTTPService struct {
	Host string `yaml:"host"` // hostname (Docker DNS or external)
	Port string `yaml:"port"` // port number
}

// PortForward defines a TCP port forward from the gateway to the agent.
type PortForward struct {
	Listen string `yaml:"listen"` // listen address (e.g., ":1455")
	Target string `yaml:"target"` // target address (e.g., "coder:1455")
}

// RequestHandler intercepts connections to specific hosts.
type RequestHandler interface {
	// Matches returns true if this handler should process the given host.
	Matches(host string) bool

	// Handle processes the intercepted connection.
	// initialData contains the TLS ClientHello already read from the client.
	Handle(clientConn net.Conn, initialData []byte, serverName string)
}

// LoadConfig reads gateway configuration from a YAML file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if cfg.Listen == "" {
		cfg.Listen = ":8443"
	}
	if cfg.DNSListen == "" {
		cfg.DNSListen = ":53"
	}

	return &cfg, nil
}
