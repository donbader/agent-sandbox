package main

import "fmt"

type ProxyConfig struct {
	SandboxID     string
	AgentName     string
	NetworkName   string
	AllowedImages []string
	MaxContainers int
	MemoryBytes   int64
	NanoCPUs      int64
	PidsLimit     int64
}

func loadConfigFromEnv() (*ProxyConfig, error) {
	return nil, fmt.Errorf("not yet implemented")
}
