package v1

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/invopop/jsonschema"
)

func generateSchema(outDir string) error {
	reflector := &jsonschema.Reflector{
		DoNotReference: true,
	}
	schema := reflector.Reflect(&config.Config{})
	schema.Title = "agent-sandbox configuration"
	schema.Description = "Configuration schema for agent-sandbox agent.yaml"

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}

	if err := os.WriteFile(filepath.Join(outDir, "schema.json"), data, 0644); err != nil {
		return err
	}

	// Also generate fleet schema
	return generateFleetSchema(outDir)
}

func generateFleetSchema(outDir string) error {
	reflector := &jsonschema.Reflector{
		DoNotReference: true,
	}
	schema := reflector.Reflect(&config.FleetConfig{})
	schema.Title = "agent-sandbox fleet configuration"
	schema.Description = "Configuration schema for agent-sandbox fleet.yaml"

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fleet schema: %w", err)
	}

	return os.WriteFile(filepath.Join(outDir, "fleet-schema.json"), data, 0644)
}
