package plugin

import "maps"

// MergeContributions combines multiple contribution sets in order.
// CapAdd entries are deduplicated across plugins. SkipUserns is a logical OR.
func MergeContributions(contribs ...*Contributions) *Contributions {
	merged := &Contributions{
		Sidecar: SidecarContrib{Services: map[string]ComposeService{}},
	}

	capSeen := make(map[string]bool)

	for _, c := range contribs {
		if c == nil {
			continue
		}
		merged.Runtime.ExtraBuilds = append(merged.Runtime.ExtraBuilds, c.Runtime.ExtraBuilds...)
		merged.Runtime.PreEntrypoint = append(merged.Runtime.PreEntrypoint, c.Runtime.PreEntrypoint...)
		merged.Runtime.Ports = append(merged.Runtime.Ports, c.Runtime.Ports...)
		merged.Runtime.Volumes = append(merged.Runtime.Volumes, c.Runtime.Volumes...)
		for _, cap := range c.Runtime.CapAdd {
			if !capSeen[cap] {
				merged.Runtime.CapAdd = append(merged.Runtime.CapAdd, cap)
				capSeen[cap] = true
			}
		}
		if c.Runtime.SkipUserns {
			merged.Runtime.SkipUserns = true
		}
		merged.Gateway.Services = append(merged.Gateway.Services, c.Gateway.Services...)
		merged.Gateway.Volumes = append(merged.Gateway.Volumes, c.Gateway.Volumes...)
		merged.Gateway.Routes = append(merged.Gateway.Routes, c.Gateway.Routes...)
		maps.Copy(merged.Sidecar.Services, c.Sidecar.Services)
	}

	return merged
}
