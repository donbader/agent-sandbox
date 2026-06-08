package plugin

// MergeContributions combines multiple contribution sets in order.
func MergeContributions(contribs ...*Contributions) *Contributions {
	merged := &Contributions{
		Sidecar: SidecarContrib{Services: map[string]ComposeService{}},
	}

	for _, c := range contribs {
		if c == nil {
			continue
		}
		merged.Runtime.ExtraBuilds = append(merged.Runtime.ExtraBuilds, c.Runtime.ExtraBuilds...)
		merged.Runtime.PreEntrypoint = append(merged.Runtime.PreEntrypoint, c.Runtime.PreEntrypoint...)
		merged.Runtime.Ports = append(merged.Runtime.Ports, c.Runtime.Ports...)
		merged.Runtime.Volumes = append(merged.Runtime.Volumes, c.Runtime.Volumes...)
		merged.Gateway.Services = append(merged.Gateway.Services, c.Gateway.Services...)
		merged.Gateway.Volumes = append(merged.Gateway.Volumes, c.Gateway.Volumes...)
		merged.Gateway.Routes = append(merged.Gateway.Routes, c.Gateway.Routes...)
		for name, svc := range c.Sidecar.Services {
			merged.Sidecar.Services[name] = svc
		}
	}

	return merged
}
