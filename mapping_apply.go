package dbunit

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/viant/dbunit/fixture"
)

type mappingFrame struct {
	root   map[string]any
	source any
	mapped map[string]any
	parent *mappingFrame
	path   string
	row    int
}
type mappingSelection struct {
	value any
	path  string
}
type mappingBucket struct {
	rows []map[string]any
	keys map[string]map[string]any
}

// Map projects concrete logical rows without evaluating expressions or generating
// identities. Its fresh inline sources are marked Expanded so Prepare preserves
// literal values. Numeric key values normalize exactly across Go widths and
// json.Number; strings, numbers, booleans and times occupy distinct key domains.
// Composite components are length-delimited. Nested column values are atomic:
// conflicting JSON objects are rejected instead of implicitly deep-merged.
func (p *MappingPlan) Map(ctx context.Context, rows []map[string]any) ([]Dataset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("mapping plan is required")
	}
	input, err := fixture.Clone(rows)
	if err != nil {
		return nil, err
	}
	buckets := make([]mappingBucket, len(p.nodes))
	for i := range buckets {
		buckets[i] = mappingBucket{rows: make([]map[string]any, 0), keys: map[string]map[string]any{}}
	}
	var project func(int, *mappingFrame, map[string]any, int) error
	project = func(index int, parent *mappingFrame, root map[string]any, logicalRow int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		node := &p.nodes[index]
		base := any(root)
		path := fmt.Sprintf("rows[%d]", logicalRow-1)
		if parent != nil {
			base = parent.source
			path = parent.path
		}
		selected, err := selectMappingValues(ctx, base, node.spec.Path, path, 0)
		if err != nil {
			return fmt.Errorf("mapping %q dataset %q logical row %d: %w", p.name, node.spec.Name, logicalRow, err)
		}
		for _, item := range selected {
			if err := ctx.Err(); err != nil {
				return err
			}
			frame := &mappingFrame{root: root, source: item.value, parent: parent, path: item.path, row: logicalRow}
			projected, skip, err := p.projectMappingRow(node, frame)
			if err != nil {
				return err
			}
			if skip {
				continue
			}
			cloned, err := fixture.Clone([]map[string]any{projected})
			if err != nil {
				return fmt.Errorf("mapping %q dataset %q at %s: %w", p.name, node.spec.Name, frame.path, err)
			}
			projected = cloned[0]
			bucket := &buckets[index]
			if len(node.spec.Keys) > 0 {
				key, err := rowKey(projected, node.spec.Keys)
				if err != nil {
					return fmt.Errorf("mapping %q dataset %q logical row %d at %s: %w", p.name, node.spec.Name, logicalRow, frame.path, err)
				}
				if existing, found := bucket.keys[key]; found {
					for _, column := range sortedKeys(projected) {
						value := projected[column]
						if previous, present := existing[column]; present {
							if !mappingValuesEqual(previous, value) {
								return &MappingConflictError{Mapping: p.name, Dataset: node.spec.Name, Row: logicalRow, Path: frame.path, Column: column, Existing: previous, Incoming: value}
							}
						} else {
							existing[column] = value
						}
					}
					projected = existing
				} else {
					bucket.keys[key] = projected
					bucket.rows = append(bucket.rows, projected)
				}
			} else {
				bucket.rows = append(bucket.rows, projected)
			}
			frame.mapped = projected
			for _, child := range node.children {
				if err := project(child, frame, root, logicalRow); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i, row := range input {
		for _, root := range p.roots {
			if err = project(root, nil, row, i+1); err != nil {
				return nil, err
			}
		}
	}
	datasets := make([]Dataset, 0, len(p.nodes))
	for _, index := range p.order {
		table := p.nodes[index].spec
		datasets = append(datasets, Dataset{Name: table.Name, Database: table.Database, Table: append(Identifier(nil), table.Table...), Source: fixture.Resolved(buckets[index].rows), Operation: table.Operation, Load: table.Load, Keys: append([]string(nil), table.Keys...), DependsOn: append([]string(nil), table.DependsOn...)})
	}
	return datasets, nil
}

func (p *MappingPlan) projectMappingRow(node *mappingNode, frame *mappingFrame) (map[string]any, bool, error) {
	row := make(map[string]any, len(node.spec.Columns))
	missing := func(column string) (map[string]any, bool, error) {
		if node.spec.OnMissingRequired == MissingRequiredSkip {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("mapping %q dataset %q logical row %d at %s: required column %q is missing or null", p.name, node.spec.Name, frame.row, frame.path, column)
	}
	for _, column := range node.spec.Columns {
		object := frame.source
		switch column.Scope {
		case MappingRoot:
			object = frame.root
		case MappingParent:
			object = frame.parent.source
		case MappingParentRow:
			object = frame.parent.mapped
		}
		var value any
		var present bool
		var err error
		if column.Scope == MappingElement {
			value = frame.source
			present = true
		} else {
			value, present, err = mappingPathValue(object, column.Path)
		}
		if err != nil {
			return nil, false, fmt.Errorf("mapping %q dataset %q logical row %d at %s column %q: %w", p.name, node.spec.Name, frame.row, frame.path, column.Name, err)
		}
		if !present && column.HasDefault {
			value = column.Default
			present = true
		}
		if column.Required && (!present || mappingNull(value)) {
			return missing(column.Name)
		}
		if present {
			row[column.Name] = value
		}
	}
	for _, key := range node.spec.Keys {
		value, present := row[key]
		if !present || mappingNull(value) {
			return missing(key)
		}
	}
	if len(row) == 0 {
		return nil, false, fmt.Errorf("mapping %q dataset %q logical row %d at %s projects an empty row", p.name, node.spec.Name, frame.row, frame.path)
	}
	return row, false, nil
}

func selectMappingValues(ctx context.Context, value any, path []string, location string, depth int) ([]mappingSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 64 {
		return nil, fmt.Errorf("mapping source path at %s exceeds nesting limit (64)", location)
	}
	reflected := mappingUnwrap(reflect.ValueOf(value))
	if !reflected.IsValid() || mappingNull(reflected.Interface()) {
		return nil, nil
	}
	if mappingCollection(reflected) {
		var result []mappingSelection
		for i := 0; i < reflected.Len(); i++ {
			selected, err := selectMappingValues(ctx, reflected.Index(i).Interface(), path, fmt.Sprintf("%s[%d]", location, i), depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, selected...)
		}
		return result, nil
	}
	if len(path) == 0 {
		return []mappingSelection{{value: reflected.Interface(), path: location}}, nil
	}
	child, present, err := mappingField(reflected, path[0])
	if err != nil {
		return nil, fmt.Errorf("source path %s: %w", location, err)
	}
	if !present {
		return nil, nil
	}
	return selectMappingValues(ctx, child, path[1:], fmt.Sprintf("%s[%q]", location, path[0]), depth+1)
}

func mappingPathValue(value any, path []string) (any, bool, error) {
	for _, part := range path {
		reflected := mappingUnwrap(reflect.ValueOf(value))
		if !reflected.IsValid() || mappingNull(reflected.Interface()) {
			return nil, false, nil
		}
		child, present, err := mappingField(reflected, part)
		if err != nil {
			return nil, false, err
		}
		if !present {
			return nil, false, nil
		}
		value = child
	}
	return value, true, nil
}

func mappingField(value reflect.Value, key string) (any, bool, error) {
	if value.Kind() != reflect.Map || value.Type().Key().Kind() != reflect.String {
		return nil, false, fmt.Errorf("path segment %q requires an object, got %s (select repeated rows with MappingTable.Path)", key, value.Type())
	}
	mapKey := reflect.ValueOf(key).Convert(value.Type().Key())
	selected := value.MapIndex(mapKey)
	if !selected.IsValid() {
		return nil, false, nil
	}
	return selected.Interface(), true, nil
}
func mappingUnwrap(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}
func mappingCollection(value reflect.Value) bool {
	return (value.Kind() == reflect.Array || value.Kind() == reflect.Slice) && value.Type().Elem().Kind() != reflect.Uint8
}
func mappingNull(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return reflected.IsNil()
	}
	return false
}
func mappingValuesEqual(a, b any) bool {
	if left, ok := number(a); ok {
		if right, ok := number(b); ok {
			return left.Cmp(right) == 0
		}
	}
	if left, ok := a.(time.Time); ok {
		if right, ok := b.(time.Time); ok {
			return left.Equal(right)
		}
	}
	return reflect.DeepEqual(a, b)
}
