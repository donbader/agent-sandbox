package proxy

import (
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

// RewriterConfig describes a rewriter to instantiate for a set of domains.
type RewriterConfig struct {
	Type        string   `yaml:"type"`                  // "telegram-url" or "auth-header"
	Domains     []string `yaml:"domains"`               // domains this rewriter applies to
	EnvVar      string   `yaml:"env_var"`               // environment variable holding the secret
	Header      string   `yaml:"header,omitempty"`      // header name to inject (auth-header only)
	ValueFormat string   `yaml:"value_format,omitempty"` // header value format, e.g. "token ${value}"
}

// Config holds gateway configuration.
type Config struct {
	Listen      string           `yaml:"listen"`       // TCP listen address (e.g., ":8443")
	DNSListen   string           `yaml:"dns_listen"`   // DNS listen address (e.g., ":53")
	MITMDomains []string         `yaml:"mitm_domains"` // domains to MITM (terminate TLS)
	CACertPath  string           `yaml:"ca_cert"`      // path to CA certificate for MITM
	CAKeyPath   string           `yaml:"ca_key"`       // path to CA private key for MITM
	Rewriters   []RewriterConfig `yaml:"rewriters"`    // rewriters to apply to intercepted requests
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
