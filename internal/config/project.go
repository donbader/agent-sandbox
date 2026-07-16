package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Project is the unified representation of any agent-sandbox project.
// Loaded from fleet.yaml + agent subdirectories.
type Project struct {
	Dir            string   // absolute path to project root
	RuntimeEngine  string   // from fleet.yaml or auto-detected
	Agents         []Agent  // always len >= 1
	SharedNetworks []string // external compose networks attached to every generated service
}

// Agent pairs a resolved config with its source directory.
type Agent struct {
	Name   string  // from Config.Name
	Dir    string  // absolute path to agent's directory
	Config *Config // fully resolved (shared merged, defaults applied)
}

// LoadProject loads fleet.yaml and all referenced agent configs from dir.
func LoadProject(dir string) (*Project, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve project dir: %w", err)
	}

	fleet, err := LoadFleet(absDir)
	if err != nil {
		return nil, err
	}

	var agents []Agent
	for _, agentName := range fleet.Agents {
		agentDir := filepath.Join(absDir, agentName)
		cfg, err := Load(agentDir)
		if err != nil {
			return nil, fmt.Errorf("loading agent %q: %w", agentName, err)
		}

		cfg.Installations = MergeInstallations(fleet.Shared.Installations, cfg.Installations)
		cfg.Gateway.Egress = MergeEgressRules(fleet.Shared.Gateway.Egress, cfg.Gateway.Egress)

		agents = append(agents, Agent{
			Name:   cfg.Name,
			Dir:    agentDir,
			Config: cfg,
		})
	}

	return &Project{Dir: absDir, RuntimeEngine: fleet.RuntimeEngine, Agents: agents, SharedNetworks: fleet.Shared.Networks}, nil
}

// ResolveAgent returns the targeted agent: uses explicit name if provided,
// otherwise returns the single agent or errors with available names.
func (p *Project) ResolveAgent(name string) (*Agent, error) {
	if name != "" {
		return p.AgentByName(name)
	}
	if len(p.Agents) == 1 {
		return &p.Agents[0], nil
	}
	names := make([]string, len(p.Agents))
	for i, a := range p.Agents {
		names[i] = a.Name
	}
	return nil, fmt.Errorf("multiple agents in project, use --agent to specify one of: %s",
		strings.Join(names, ", "))
}

// AgentByName returns the agent with the given name, or an error listing available agents.
func (p *Project) AgentByName(name string) (*Agent, error) {
	for i := range p.Agents {
		if p.Agents[i].Name == name {
			return &p.Agents[i], nil
		}
	}
	names := make([]string, len(p.Agents))
	for i, a := range p.Agents {
		names[i] = a.Name
	}
	return nil, fmt.Errorf("agent %q not found, available agents: %s",
		name, strings.Join(names, ", "))
}
