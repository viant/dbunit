package dbunit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
)

// Mapping projects one logical fixture into explicitly named physical datasets.
// It has no mutable registry, expression evaluator or database connection.
type Mapping struct {
	Name   string         `json:"name" yaml:"name"`
	Tables []MappingTable `json:"tables" yaml:"tables"`
}

// MappingScope identifies the object used by a column path. Parent is the
// nearest association's source; ParentRow is its projected physical row. Root is
// the original logical record. Element projects an entire repeated scalar (or
// object) and requires an empty path.
type MappingScope string

const (
	MappingSource    MappingScope = "source"
	MappingRoot      MappingScope = "root"
	MappingParent    MappingScope = "parent"
	MappingParentRow MappingScope = "parentRow"
	MappingElement   MappingScope = "element"
)

type MissingRequiredPolicy string

const (
	MissingRequiredError MissingRequiredPolicy = "error"
	MissingRequiredSkip  MissingRequiredPolicy = "skip"
)

// MappingTable selects objects or repeated elements using literal path segments.
// Paths are relative to the logical root for top-level tables and to the nearest
// parent's selected object for associations. Arrays are traversed in input order.
// Missing/null paths and empty collections emit no rows. A skipped required row
// skips its descendants. Associations depend on the parent table automatically;
// other physical prerequisites are explicit DependsOn names.
//
// Keys enable row deduplication. Without Keys every selected object emits a row.
// Duplicate keys merge absent columns; conflicting present values are errors.
// Every table yields a Dataset, including an empty one (so Replace can clear it).
type MappingTable struct {
	Name              string                `json:"name" yaml:"name"`
	Table             Identifier            `json:"table" yaml:"table"`
	Path              []string              `json:"path,omitempty" yaml:"path,omitempty"`
	Columns           []MappingColumn       `json:"columns" yaml:"columns"`
	Keys              []string              `json:"keys,omitempty" yaml:"keys,omitempty"`
	DependsOn         []string              `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
	Associations      []MappingTable        `json:"associations,omitempty" yaml:"associations,omitempty"`
	Database          string                `json:"database,omitempty" yaml:"database,omitempty"`
	Operation         Operation             `json:"operation,omitempty" yaml:"operation,omitempty"`
	Load              LoadOptions           `json:"load,omitempty" yaml:"load,omitempty"`
	OnMissingRequired MissingRequiredPolicy `json:"onMissingRequired,omitempty" yaml:"onMissingRequired,omitempty"`
}

// MappingColumn preserves absent/null/zero distinctions. An empty Path defaults
// to [Name], except for Element. Defaults are literal values applied only when
// the source field is absent. HasDefault permits an explicit null default in Go;
// JSON decoding sets it when a default property is present. Required means
// present and non-null, including false, zero and the empty string.
type MappingColumn struct {
	Name       string       `json:"name" yaml:"name"`
	Path       []string     `json:"path,omitempty" yaml:"path,omitempty"`
	Scope      MappingScope `json:"scope,omitempty" yaml:"scope,omitempty"`
	Default    any          `json:"default,omitempty" yaml:"default,omitempty"`
	HasDefault bool         `json:"hasDefault,omitempty" yaml:"hasDefault,omitempty"`
	Required   bool         `json:"required,omitempty" yaml:"required,omitempty"`
}

func (c *MappingColumn) UnmarshalJSON(content []byte) error {
	type plain MappingColumn
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(content, &properties); err != nil {
		return err
	}
	_, present := properties["default"]
	decoded.HasDefault = decoded.HasDefault || present
	*c = MappingColumn(decoded)
	return nil
}

func (c *MappingColumn) UnmarshalYAML(node *yaml.Node) error {
	type plain MappingColumn
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "default" {
			decoded.HasDefault = true
		}
	}
	*c = MappingColumn(decoded)
	return nil
}

var ErrMappingConflict = errors.New("conflicting mapped row")

// MappingConflictError identifies a conflicting physical column without hiding
// either value. No output datasets are returned after a conflict.
type MappingConflictError struct {
	Mapping  string
	Dataset  string
	Row      int
	Path     string
	Column   string
	Existing any
	Incoming any
}

func (e *MappingConflictError) Error() string {
	return fmt.Sprintf("mapping %q dataset %q logical row %d at %s column %q: %v", e.Mapping, e.Dataset, e.Row, e.Path, e.Column, ErrMappingConflict)
}
func (e *MappingConflictError) Unwrap() error { return ErrMappingConflict }

type mappingNode struct {
	spec                   MappingTable
	parent                 int
	children, dependencies []int
}

// MappingPlan is an immutable compiled mapping. Concurrent Map calls share no
// rows, deduplication state, defaults or input references.
type MappingPlan struct {
	name         string
	nodes        []mappingNode
	roots, order []int
}
