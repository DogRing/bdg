package worldgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

func Parse(blob []byte) (Fixture, error) {
	var fx Fixture
	dec := yaml.NewDecoder(bytes.NewReader(blob))
	dec.KnownFields(true)
	if err := dec.Decode(&fx); err != nil {
		return Fixture{}, fmt.Errorf("worldgen: parse fixture: %w", err)
	}
	if err := validateFixtureShape(fx); err != nil {
		return Fixture{}, err
	}
	return fx, nil
}

func ParseFile(path, schemaPath string) (Fixture, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("worldgen: read fixture %s: %w", path, err)
	}
	if schemaPath != "" {
		schema, err := os.ReadFile(schemaPath)
		if err != nil {
			return Fixture{}, fmt.Errorf("worldgen: read fixture schema %s: %w", schemaPath, err)
		}
		if err := validateYAML(blob, schema, "fixture"); err != nil {
			return Fixture{}, err
		}
	}
	return Parse(blob)
}

func Encode(fx Fixture) ([]byte, error) {
	if err := validateFixtureShape(fx); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(fx)
	if err != nil {
		return nil, fmt.Errorf("worldgen: encode fixture: %w", err)
	}
	return out, nil
}

func validateFixtureShape(fx Fixture) error {
	if fx.SchemaVersion != 1 {
		return fmt.Errorf("worldgen: fixture schema_version %d, want 1", fx.SchemaVersion)
	}
	if fx.Bounds != nil && (fx.Bounds.Max[0] <= fx.Bounds.Min[0] || fx.Bounds.Max[1] <= fx.Bounds.Min[1]) {
		return fmt.Errorf("worldgen: fixture bounds max must exceed min")
	}
	if fx.Terrain != nil {
		if fx.Terrain.Random {
			// Random and explicit layouts are mutually exclusive (SPEC §Fixture; the
			// loader materializes the cells + elevation itself).
			if fx.Terrain.Cols != 0 || fx.Terrain.Rows != 0 || len(fx.Terrain.Cells) != 0 || len(fx.Terrain.Elevation) != 0 {
				return fmt.Errorf("worldgen: terrain random:true excludes explicit cols/rows/cells/elevation")
			}
		} else {
			if fx.Terrain.Cols <= 0 || fx.Terrain.Rows <= 0 {
				return fmt.Errorf("worldgen: terrain cols/rows must be positive")
			}
			if len(fx.Terrain.Cells) != fx.Terrain.Cols*fx.Terrain.Rows {
				return fmt.Errorf("worldgen: terrain cells len %d != cols*rows %d", len(fx.Terrain.Cells), fx.Terrain.Cols*fx.Terrain.Rows)
			}
			if len(fx.Terrain.Elevation) != 0 && len(fx.Terrain.Elevation) != fx.Terrain.Cols*fx.Terrain.Rows {
				return fmt.Errorf("worldgen: terrain elevation len %d != cols*rows %d", len(fx.Terrain.Elevation), fx.Terrain.Cols*fx.Terrain.Rows)
			}
		}
	}
	return nil
}

func validateYAML(yamlData, schemaData []byte, sourceName string) error {
	schema, err := jsonschema.CompileString(sourceName, string(schemaData))
	if err != nil {
		return fmt.Errorf("worldgen: compile schema %s: %w", sourceName, err)
	}
	var raw any
	if err := yaml.Unmarshal(yamlData, &raw); err != nil {
		return fmt.Errorf("worldgen: yaml unmarshal: %w", err)
	}
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("worldgen: json marshal: %w", err)
	}
	var doc any
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return fmt.Errorf("worldgen: json unmarshal: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("worldgen: schema %s: %w", sourceName, err)
	}
	return nil
}
