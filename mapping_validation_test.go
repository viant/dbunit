package dbunit

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func simpleMappingSpec() Mapping {
	return Mapping{Name: "validation", Tables: []MappingTable{{Name: "rows", Table: Table("rows"), Columns: []MappingColumn{{Name: "id"}}, Keys: []string{"id"}}}}
}

func TestMappingCompileRejectsAmbiguousGraphs(t *testing.T) {
	cases := []struct {
		name, expected string
		mutate         func(*Mapping)
	}{
		{"missing mapping name", "name is required", func(m *Mapping) { m.Name = "" }},
		{"no tables", "requires tables", func(m *Mapping) { m.Tables = nil }},
		{"missing table name", "table name is required", func(m *Mapping) { m.Tables[0].Name = "" }},
		{"missing destination", "requires a destination", func(m *Mapping) { m.Tables[0].Table = nil }},
		{"invalid destination", "invalid destination", func(m *Mapping) { m.Tables[0].Table = Table("bad\x00name") }},
		{"missing columns", "requires column", func(m *Mapping) { m.Tables[0].Columns = nil }},
		{"duplicate columns", "duplicate column", func(m *Mapping) { m.Tables[0].Columns = append(m.Tables[0].Columns, MappingColumn{Name: "id"}) }},
		{"missing key projection", "no column projection", func(m *Mapping) { m.Tables[0].Keys = []string{"unmapped"} }},
		{"duplicate key", "duplicate key", func(m *Mapping) { m.Tables[0].Keys = []string{"id", "id"} }},
		{"unknown scope", "unknown scope", func(m *Mapping) { m.Tables[0].Columns[0].Scope = "guess" }},
		{"parent outside association", "parent outside", func(m *Mapping) { m.Tables[0].Columns[0].Scope = MappingParentRow }},
		{"element with path", "empty path", func(m *Mapping) {
			m.Tables[0].Columns[0].Scope = MappingElement
			m.Tables[0].Columns[0].Path = []string{"id"}
		}},
		{"unknown required policy", "unknown missing-required", func(m *Mapping) { m.Tables[0].OnMissingRequired = "guess" }},
		{"unknown operation", "unsupported operation", func(m *Mapping) { m.Tables[0].Operation = "truncate" }},
		{"upsert without key", "requires keys", func(m *Mapping) { m.Tables[0].Operation = Upsert; m.Tables[0].Keys = nil }},
		{"negative batch", "negative batch", func(m *Mapping) { m.Tables[0].Load.BatchSize = -1 }},
		{"unknown strategy", "unknown load strategy", func(m *Mapping) { m.Tables[0].Load.Strategy = "guess" }},
		{"unknown dependency", "unknown dependency", func(m *Mapping) { m.Tables[0].DependsOn = []string{"absent"} }},
		{"self dependency", "dependency cycle", func(m *Mapping) { m.Tables[0].DependsOn = []string{"rows"} }},
		{"association dependency cycle", "dependency cycle", func(m *Mapping) {
			m.Tables[0].DependsOn = []string{"child"}
			m.Tables[0].Associations = []MappingTable{{Name: "child", Table: Table("child"), Columns: []MappingColumn{{Name: "id"}}}}
		}},
		{"duplicate name", "duplicate mapping table name", func(m *Mapping) {
			m.Tables = append(m.Tables, MappingTable{Name: "rows", Table: Table("other"), Columns: []MappingColumn{{Name: "id"}}})
		}},
		{"duplicate physical target", "same physical table", func(m *Mapping) {
			m.Tables = append(m.Tables, MappingTable{Name: "other", Table: Table("rows"), Columns: []MappingColumn{{Name: "id"}}})
		}},
		{"cyclic default", "cycle", func(m *Mapping) {
			value := map[string]any{}
			value["self"] = value
			m.Tables[0].Columns[0].Default = value
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := simpleMappingSpec()
			tc.mutate(&spec)
			_, err := CompileMapping(spec)
			if err == nil || !strings.Contains(err.Error(), tc.expected) {
				t.Fatal(err)
			}
		})
	}
	// One identifier containing a dot and a qualified path are distinct targets.
	spec := simpleMappingSpec()
	spec.Tables[0].Table = Table("a.b")
	spec.Tables = append(spec.Tables, MappingTable{Name: "qualified", Table: Table("a", "b"), Columns: []MappingColumn{{Name: "id"}}})
	if _, err := CompileMapping(spec); err != nil {
		t.Fatal(err)
	}
}

func TestMappingSourceShapeAndCancellation(t *testing.T) {
	spec := simpleMappingSpec()
	spec.Tables[0].Path = []string{"objects"}
	plan := compileTestMapping(t, spec)
	for _, source := range []map[string]any{{}, {"objects": nil}, {"objects": []any{}}} {
		got := mappedRows(t, plan, source)
		if len(got) != 1 || len(got[0].Source.Data) != 0 {
			t.Fatal(got)
		}
	}
	_, err := plan.Map(context.Background(), []map[string]any{{"objects": []any{42}}})
	if err == nil || !strings.Contains(err.Error(), "requires an object") {
		t.Fatal(err)
	}
	spec = simpleMappingSpec()
	spec.Tables[0].Columns[0].Path = []string{"objects", "id"}
	plan = compileTestMapping(t, spec)
	_, err = plan.Map(context.Background(), []map[string]any{{"objects": []any{map[string]any{"id": 1}}}})
	if err == nil || !strings.Contains(err.Error(), "select repeated rows") {
		t.Fatal(err)
	}
	plan = compileTestMapping(t, simpleMappingSpec())
	_, err = plan.Map(context.Background(), []map[string]any{{"id": map[string]any{"nested": 1}}})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
	_, err = plan.Map(context.Background(), []map[string]any{{"id": math.NaN()}})
	if err == nil {
		t.Fatal("nonfinite key accepted")
	}
	_, err = plan.Map(context.Background(), []map[string]any{nil})
	if err == nil {
		t.Fatal("null logical row accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = plan.Map(canceled, []map[string]any{{"id": 1}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var missing *MappingPlan
	_, err = missing.Map(context.Background(), nil)
	if err == nil {
		t.Fatal("nil plan accepted")
	}
}

func TestMappingYAMLExplicitNullDefault(t *testing.T) {
	var spec Mapping
	err := yaml.Unmarshal([]byte(`name: yaml
tables:
  - name: rows
    table: [rows]
    columns:
      - name: id
      - name: nullable
        default: null
`), &spec)
	if err != nil {
		t.Fatal(err)
	}
	output := mappedRows(t, compileTestMapping(t, spec), map[string]any{"id": 1})
	if value, present := output[0].Source.Data[0]["nullable"]; !present || value != nil {
		t.Fatal(output)
	}
}
