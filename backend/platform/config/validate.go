// Package config schema validation — lightweight YAML structural validation
// against JSON Schema. Uses github.com/santhosh-tekuri/jsonschema/v5 for production.
//
// The primary validation flow:
//  1. Unmarshal the YAML into a generic map.
//  2. Marshal the map to JSON.
//  3. Validate the JSON against the compiled schema.
//
// This validates schema_version const values, required fields, value types,
// min/max bounds, enum constraints, and pattern constraints.
package config

import (
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// validateYAML validates a YAML document against a JSON Schema (as raw bytes).
// It returns a descriptive error on the first violation, or nil if valid.
func validateYAML(yamlData, schemaData []byte, sourceName string) error {
	// Compile the schema.
	schema, err := jsonschema.CompileString(sourceName, string(schemaData))
	if err != nil {
		return fmt.Errorf("compile schema %s: %w", sourceName, err)
	}

	// Unmarshal YAML into generic form.
	var raw any
	if err := yaml.Unmarshal(yamlData, &raw); err != nil {
		return fmt.Errorf("yaml unmarshal: %w", err)
	}

	// Convert YAML to JSON-compatible form. jsonschema/v5 works on JSON
	// decoded values; YAML parsing may produce slightly different types
	// (e.g. int vs float64). Convert through JSON round-trip.
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}

	var doc any
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return fmt.Errorf("json unmarshal: %w", err)
	}

	// Validate.
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	return nil
}
