package plugin

import "fmt"

// ResolveOrder returns plugins sorted topologically so dependencies come before
// dependents. Returns an error if a cycle is detected.
// The nameIndex maps both full refs (e.g. "@builtin/agent-docker") and short
// names (for @builtin/ plugins) to the same PluginDef.
func ResolveOrder(plugins []*PluginDef, nameIndex map[string]*PluginDef) ([]*PluginDef, error) {
	if len(plugins) <= 1 {
		return plugins, nil
	}

	// Build adjacency: plugin name → set of dependency names.
	// Use PluginDef pointer as identity.
	type node struct {
		def   *PluginDef
		inDeg int
		depOf []*PluginDef // plugins that depend on this one (reverse edges)
	}

	nodes := make(map[*PluginDef]*node, len(plugins))
	for _, p := range plugins {
		nodes[p] = &node{def: p}
	}

	// Build edges from requirements.
	for _, p := range plugins {
		for _, reqStr := range p.Requires {
			req, err := ParseRequirement(reqStr)
			if err != nil {
				return nil, fmt.Errorf("plugin %q: %w", p.Name, err)
			}
			dep := nameIndex[req.Name]
			if dep == nil {
				continue // missing dep — validated elsewhere
			}
			if dep == p {
				continue // self-dependency, skip
			}
			// p depends on dep → dep must come before p.
			nodes[p].inDeg++
			nodes[dep].depOf = append(nodes[dep].depOf, p)
		}
	}

	// Kahn's algorithm.
	var queue []*PluginDef
	for _, n := range nodes {
		if n.inDeg == 0 {
			queue = append(queue, n.def)
		}
	}

	var sorted []*PluginDef
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, cur)

		for _, dependent := range nodes[cur].depOf {
			nodes[dependent].inDeg--
			if nodes[dependent].inDeg == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(sorted) != len(plugins) {
		// Cycle detected — find participants.
		var cycle []string
		for _, n := range nodes {
			if n.inDeg > 0 {
				cycle = append(cycle, n.def.Name)
			}
		}
		return nil, fmt.Errorf("dependency cycle detected among plugins: %v", cycle)
	}

	return sorted, nil
}
