// Package manifest decodes explicit, versioned dbunit cases. File references
// remain relative to the configured fixture resolver, never the test name.
package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/viant/dbunit"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/toolbox/data"
	"gopkg.in/yaml.v3"
)

type Document struct {
	Version int                  `json:"version"`
	Name    string               `json:"name,omitempty"`
	State   data.Map             `json:"state,omitempty"`
	Schema  []dbunit.Statement   `json:"schema,omitempty"`
	Prepare []dbunit.Dataset     `json:"prepare,omitempty"`
	Expect  []dbunit.Expectation `json:"expect,omitempty"`
	Cleanup []dbunit.Statement   `json:"cleanup,omitempty"`
}

// Load reads a manifest from an explicit source. The caller may attach a Go Act
// callback to the returned case. SQL source files use Engine.ApplySchema.
func Load(ctx context.Context, resolver fixture.Resolver, source fixture.Source) (dbunit.Case, error) {
	if resolver == nil {
		return dbunit.Case{}, fmt.Errorf("manifest resolver is required")
	}
	content, err := resolver.Read(ctx, source)
	if err != nil {
		return dbunit.Case{}, err
	}
	format := strings.ToLower(source.Format)
	if format == "" {
		name := source.Path
		if name == "" {
			name = source.URL
		}
		format = strings.TrimPrefix(path.Ext(name), ".")
	}
	if format == "yaml" || format == "yml" {
		var doc yaml.Node
		d := yaml.NewDecoder(bytes.NewReader(content))
		if err = d.Decode(&doc); err != nil {
			return dbunit.Case{}, fmt.Errorf("manifest YAML: %w", err)
		}
		var extra yaml.Node
		if err = d.Decode(&extra); err != io.EOF {
			return dbunit.Case{}, fmt.Errorf("manifest must contain one YAML document")
		}
		value, err := yamlValue(&doc, 0)
		if err != nil {
			return dbunit.Case{}, err
		}
		content, err = json.Marshal(value)
		if err != nil {
			return dbunit.Case{}, err
		}
	} else if format != "json" {
		return dbunit.Case{}, fmt.Errorf("manifest format must be json or yaml")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var doc Document
	if err := decoder.Decode(&doc); err != nil {
		return dbunit.Case{}, fmt.Errorf("manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return dbunit.Case{}, fmt.Errorf("manifest contains trailing JSON")
	}
	if doc.Version != 1 {
		return dbunit.Case{}, fmt.Errorf("unsupported manifest version %d (expected 1)", doc.Version)
	}
	return dbunit.Case{Name: doc.Name, State: doc.State, Schema: doc.Schema, Prepare: doc.Prepare, Expect: doc.Expect, Cleanup: doc.Cleanup}, nil
}

// YAML is converted through nodes to retain exact numeric lexemes. Aliases and
// merge keys are rejected rather than allowing hidden or unbounded expansion.
func yamlValue(n *yaml.Node, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("manifest exceeds nesting limit")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, fmt.Errorf("manifest must contain one document")
		}
		return yamlValue(n.Content[0], depth+1)
	case yaml.MappingNode:
		result := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return nil, fmt.Errorf("manifest line %d: mapping keys must be strings", k.Line)
			}
			if _, ok := result[k.Value]; ok {
				return nil, fmt.Errorf("manifest line %d: duplicate key %q", k.Line, k.Value)
			}
			v, err := yamlValue(n.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			result[k.Value] = v
		}
		return result, nil
	case yaml.SequenceNode:
		result := make([]any, len(n.Content))
		for i, c := range n.Content {
			v, err := yamlValue(c, depth+1)
			if err != nil {
				return nil, err
			}
			result[i] = v
		}
		return result, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!timestamp":
			return n.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			return strings.EqualFold(n.Value, "true"), nil
		case "!!int", "!!float":
			value := strings.ReplaceAll(n.Value, "_", "")
			if strings.HasPrefix(value, "+") {
				value = value[1:]
			}
			if !json.Valid([]byte(value)) {
				return nil, fmt.Errorf("manifest line %d: use a JSON-compatible number or quote %q", n.Line, n.Value)
			}
			return json.Number(value), nil
		default:
			return nil, fmt.Errorf("manifest line %d: unsupported YAML tag %q", n.Line, n.Tag)
		}
	default:
		return nil, fmt.Errorf("manifest line %d: aliases are unsupported", n.Line)
	}
}
