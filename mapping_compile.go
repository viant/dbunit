package dbunit

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/viant/dbunit/fixture"
)

// CompileMapping validates and snapshots a deterministic dependency graph. Cycles
// and duplicate physical destinations are rejected before any fixture is read.
// Separate projections to one table must be expressed as one mapping node.
func CompileMapping(spec Mapping) (*MappingPlan, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("mapping name is required")
	}
	if len(spec.Tables) == 0 {
		return nil, fmt.Errorf("mapping %q requires tables", spec.Name)
	}
	plan := &MappingPlan{name: spec.Name}
	names := map[string]int{}
	targets := map[string]string{}
	var add func(MappingTable, int, int) (int, error)
	add = func(table MappingTable, parent, depth int) (int, error) {
		if depth > 64 {
			return 0, fmt.Errorf("mapping associations exceed nesting limit (64) or contain a cycle")
		}
		if strings.TrimSpace(table.Name) == "" {
			return 0, fmt.Errorf("mapping table name is required")
		}
		if _, exists := names[table.Name]; exists {
			return 0, fmt.Errorf("duplicate mapping table name %q", table.Name)
		}
		if len(table.Table) == 0 {
			return 0, fmt.Errorf("mapping table %q requires a destination", table.Name)
		}
		for _, part := range table.Table {
			if part == "" || strings.ContainsRune(part, 0) || !utf8.ValidString(part) {
				return 0, fmt.Errorf("mapping table %q has invalid destination identifier", table.Name)
			}
		}
		target, _ := json.Marshal(struct {
			Database string
			Table    Identifier
		}{table.Database, table.Table})
		if existing, exists := targets[string(target)]; exists {
			return 0, fmt.Errorf("mapping tables %q and %q write the same physical table; use one mapping node", existing, table.Name)
		}
		targets[string(target)] = table.Name
		if len(table.Columns) == 0 {
			return 0, fmt.Errorf("mapping table %q requires column projections", table.Name)
		}
		if table.Operation == "" {
			table.Operation = Insert
		}
		switch table.Operation {
		case Insert, Replace:
		case Upsert, DeleteMatching:
			if len(table.Keys) == 0 {
				return 0, fmt.Errorf("mapping table %q operation %s requires keys", table.Name, table.Operation)
			}
		default:
			return 0, fmt.Errorf("mapping table %q has unsupported operation %q", table.Name, table.Operation)
		}
		if table.Load.BatchSize < 0 {
			return 0, fmt.Errorf("mapping table %q has negative batch size", table.Name)
		}
		switch table.Load.Strategy {
		case "", Auto, Batch, BulkRequired:
		default:
			return 0, fmt.Errorf("mapping table %q has unknown load strategy %q", table.Name, table.Load.Strategy)
		}
		if table.OnMissingRequired == "" {
			table.OnMissingRequired = MissingRequiredError
		}
		if table.OnMissingRequired != MissingRequiredError && table.OnMissingRequired != MissingRequiredSkip {
			return 0, fmt.Errorf("mapping table %q has unknown missing-required policy %q", table.Name, table.OnMissingRequired)
		}
		table.Table = append(Identifier(nil), table.Table...)
		table.Path = append([]string(nil), table.Path...)
		table.Keys = append([]string(nil), table.Keys...)
		table.DependsOn = append([]string(nil), table.DependsOn...)
		columns := make([]MappingColumn, len(table.Columns))
		seenColumns := map[string]bool{}
		for i, col := range table.Columns {
			if col.Name == "" || strings.ContainsRune(col.Name, 0) || !utf8.ValidString(col.Name) {
				return 0, fmt.Errorf("mapping table %q has invalid column name", table.Name)
			}
			if seenColumns[col.Name] {
				return 0, fmt.Errorf("mapping table %q has duplicate column %q", table.Name, col.Name)
			}
			seenColumns[col.Name] = true
			if col.Scope == "" {
				col.Scope = MappingSource
			}
			switch col.Scope {
			case MappingSource, MappingRoot:
			case MappingParent, MappingParentRow:
				if parent < 0 {
					return 0, fmt.Errorf("mapping table %q column %q references a parent outside an association", table.Name, col.Name)
				}
			case MappingElement:
				if len(col.Path) > 0 {
					return 0, fmt.Errorf("mapping table %q column %q element scope requires an empty path", table.Name, col.Name)
				}
			default:
				return 0, fmt.Errorf("mapping table %q column %q has unknown scope %q", table.Name, col.Name, col.Scope)
			}
			col.Path = append([]string(nil), col.Path...)
			if len(col.Path) == 0 && col.Scope != MappingElement {
				col.Path = []string{col.Name}
			}
			col.HasDefault = col.HasDefault || col.Default != nil
			if col.HasDefault {
				cloned, err := fixture.Clone([]map[string]any{{"value": col.Default}})
				if err != nil {
					return 0, fmt.Errorf("mapping table %q column %q default: %w", table.Name, col.Name, err)
				}
				col.Default = cloned[0]["value"]
			}
			columns[i] = col
		}
		table.Columns = columns
		seenKeys := map[string]bool{}
		for _, key := range table.Keys {
			if !seenColumns[key] {
				return 0, fmt.Errorf("mapping table %q key %q has no column projection", table.Name, key)
			}
			if seenKeys[key] {
				return 0, fmt.Errorf("mapping table %q has duplicate key %q", table.Name, key)
			}
			seenKeys[key] = true
		}
		associations := table.Associations
		table.Associations = nil
		index := len(plan.nodes)
		names[table.Name] = index
		plan.nodes = append(plan.nodes, mappingNode{spec: table, parent: parent})
		if parent >= 0 {
			parentName := plan.nodes[parent].spec.Name
			found := false
			for _, name := range table.DependsOn {
				found = found || name == parentName
			}
			if !found {
				plan.nodes[index].spec.DependsOn = append(plan.nodes[index].spec.DependsOn, parentName)
			}
		}
		for _, association := range associations {
			child, err := add(association, index, depth+1)
			if err != nil {
				return 0, err
			}
			plan.nodes[index].children = append(plan.nodes[index].children, child)
		}
		return index, nil
	}
	for _, table := range spec.Tables {
		root, err := add(table, -1, 0)
		if err != nil {
			return nil, err
		}
		plan.roots = append(plan.roots, root)
	}
	for index := range plan.nodes {
		seen := map[string]bool{}
		for _, name := range plan.nodes[index].spec.DependsOn {
			if seen[name] {
				continue
			}
			seen[name] = true
			dependency, exists := names[name]
			if !exists {
				return nil, fmt.Errorf("mapping table %q has unknown dependency %q", plan.nodes[index].spec.Name, name)
			}
			plan.nodes[index].dependencies = append(plan.nodes[index].dependencies, dependency)
		}
	}
	visited := make([]byte, len(plan.nodes))
	var visit func(int) error
	visit = func(index int) error {
		if visited[index] == 2 {
			return nil
		}
		if visited[index] == 1 {
			return fmt.Errorf("mapping dependency cycle at %q", plan.nodes[index].spec.Name)
		}
		visited[index] = 1
		for _, dependency := range plan.nodes[index].dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visited[index] = 2
		plan.order = append(plan.order, index)
		return nil
	}
	for index := range plan.nodes {
		if err := visit(index); err != nil {
			return nil, err
		}
	}
	return plan, nil
}
