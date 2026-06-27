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
	AuthHeaders  []AuthHeader  `yaml:"auth_headers"`  // header injection rules from config
	EgressRules  []EgressRule  `yaml:"egress_rules"`  // ordered egress access control rules
}

// AuthHeader defines a header to inject on requests to a specific domain.
type AuthHeader struct {
	Domain string `yaml:"domain"` // target domain (e.g., "api.github.com")
	Header string `yaml:"header"` // header name (e.g., "Authorization")
	Value  string `yaml:"value"`  // header value (e.g., "Bearer token123")
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

// EgressRule defines an egress access control rule at the gateway runtime level.
type EgressRule struct {
	Hosts       []string          `yaml:"hosts"`                 // host patterns (domain globs, CIDRs, "*")
	Deny        bool              `yaml:"deny,omitempty"`        // if true, block matching traffic
	Headers     map[string]string `yaml:"headers,omitempty"`     // headers to inject (implies allow + MITM)
	DenyPaths   []string          `yaml:"deny_paths,omitempty"`  // URL path patterns to block
	DenyGraphQL *DenyGraphQL      `yaml:"deny_graphql,omitempty"` // block specific GraphQL mutations
	Target      string            `yaml:"target,omitempty"`      // forwarding destination (host:port)
}

// DenyGraphQL configures GraphQL mutation blocking for an egress rule.
type DenyGraphQL struct {
	Mutations []string `yaml:"mutations"` // mutation names to block (case-insensitive)
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
