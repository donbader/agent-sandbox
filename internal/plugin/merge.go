package plugin

import (
	"fmt"
	"maps"
)

// MergeContributions combines multiple contribution sets in order.
// CapAdd entries are deduplicated across plugins. SkipUserns is a logical OR.
// Returns an error if two plugins declare a sidecar service with the same name.
func MergeContributions(contribs ...*Contributions) (*Contributions, error) {
	merged := &Contributions{
		Sidecar: SidecarContrib{Services: map[string]ComposeService{}},
	}

	capSeen := make(map[string]bool)

	for _, c := range contribs {
		if c == nil {
			continue
		}
		merged.Runtime.BuildStages = append(merged.Runtime.BuildStages, c.Runtime.BuildStages...)
		merged.Runtime.ExtraBuilds = append(merged.Runtime.ExtraBuilds, c.Runtime.ExtraBuilds...)
		merged.Runtime.PreEntrypoint = append(merged.Runtime.PreEntrypoint, c.Runtime.PreEntrypoint...)
		merged.Runtime.Ports = append(merged.Runtime.Ports, c.Runtime.Ports...)
		merged.Runtime.NamespacedVolumes = append(merged.Runtime.NamespacedVolumes, c.Runtime.NamespacedVolumes...)
		merged.Runtime.RawVolumes = append(merged.Runtime.RawVolumes, c.Runtime.RawVolumes...)
		if len(c.Runtime.Environment) > 0 {
			if merged.Runtime.Environment == nil {
				merged.Runtime.Environment = make(map[string]string)
			}
			maps.Copy(merged.Runtime.Environment, c.Runtime.Environment)
		}
		for _, cap := range c.Runtime.CapAdd {
			if !capSeen[cap] {
				merged.Runtime.CapAdd = append(merged.Runtime.CapAdd, cap)
				capSeen[cap] = true
			}
		}
		if c.Runtime.SkipUserns {
			merged.Runtime.SkipUserns = true
		}
		merged.Gateway.Egress = append(merged.Gateway.Egress, c.Gateway.Egress...)
		merged.Gateway.Ingress = append(merged.Gateway.Ingress, c.Gateway.Ingress...)
		merged.Gateway.NamespacedVolumes = append(merged.Gateway.NamespacedVolumes, c.Gateway.NamespacedVolumes...)
		merged.Gateway.RawVolumes = append(merged.Gateway.RawVolumes, c.Gateway.RawVolumes...)
		merged.Gateway.Routes = append(merged.Gateway.Routes, c.Gateway.Routes...)
		for name, svc := range c.Sidecar.Services {
			if _, exists := merged.Sidecar.Services[name]; exists {
				return nil, fmt.Errorf("sidecar service name %q is declared by multiple plugins", name)
			}
			merged.Sidecar.Services[name] = svc
		}
	}

	return merged, nil
}
