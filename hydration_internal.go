package dbunit

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/dbunit/fixture"
	"github.com/viant/structology"
	"github.com/viant/toolbox/data"
)

type hydrationInput struct {
	spec     HydrationTable
	rows     structology.Maps
	resolved []*hydrationRow
}
type hydrationRow struct {
	row        map[string]any
	table      string
	index      int
	tag        string
	references []hydrationReference
	status     int
}
type hydrationReference struct {
	spec RecordReference
	tag  string
}
type hydrationSession struct {
	state          data.Map
	sequences      map[string]FixtureSequenceState
	uuid           func() (string, error)
	tags           map[string]map[string]*hydrationRow
	expansion      *hydrationExpansion
	sourceExpanded bool
}
type hydrationExpansion struct{ failure error }

func newHydrationExpansion(state data.Map) *hydrationExpansion {
	e := &hydrationExpansion{}
	registry := data.Map{}
	switch values := state[data.UDFKey].(type) {
	case data.Map:
		for k, v := range values {
			registry[k] = v
		}
	case map[string]any:
		for k, v := range values {
			registry[k] = v
		}
	}
	for name, value := range registry {
		if fn, ok := value.(func(any, data.Map) (any, error)); ok {
			registry[name] = func(argument any, s data.Map) (any, error) {
				result, err := fn(argument, s)
				if err != nil && e.failure == nil {
					e.failure = fmt.Errorf("UDF %s: %w", name, err)
				}
				return result, err
			}
		}
	}
	state[data.UDFKey] = registry
	return e
}
func (e *hydrationExpansion) expand(state data.Map, value any) (any, error) {
	e.failure = nil
	result, err := expandValue(state, hydrationContainers(value), false)
	if err != nil {
		return nil, err
	}
	if e.failure != nil {
		return nil, e.failure
	}
	return result, nil
}
func (e *Engine) hydrationInputs(ctx context.Context, req HydrateRequest) ([]hydrationInput, error) {
	type captureDestination struct {
		path   []string
		append bool
	}
	var destinations []captureDestination
	checkCapture := func(path string, appendMode bool) error {
		parts, err := hydrationStatePath(path)
		if err != nil {
			return err
		}
		for _, prior := range destinations {
			if hydrationPathsOverlap(parts, prior.path) {
				if strings.Join(parts, ".") == strings.Join(prior.path, ".") && appendMode && prior.append {
					continue
				}
				return fmt.Errorf("overlapping capture destination %q", path)
			}
		}
		destinations = append(destinations, captureDestination{parts, appendMode})
		return nil
	}
	for _, capture := range req.StateCaptures {
		if _, err := hydrationStatePath(capture.From); err != nil {
			return nil, err
		}
		if err := checkCapture(capture.Path, false); err != nil {
			return nil, err
		}
	}
	inputs := make([]hydrationInput, len(req.Tables))
	names := map[string]bool{}
	sequenceNames := map[string]bool{}
	for _, s := range req.Sequences {
		sequenceNames[s.Name] = true
	}
	maxRows := req.MaxRows
	if maxRows < 0 {
		return nil, fmt.Errorf("hydration MaxRows must not be negative")
	}
	if maxRows <= 0 {
		maxRows = e.maxRows
	}
	count := 0
	for i, table := range req.Tables {
		if table.Name == "" && len(table.Table) == 1 {
			table.Name = table.Table[0]
		}
		if table.Name == "" {
			return nil, fmt.Errorf("hydration table requires a name")
		}
		if names[table.Name] {
			return nil, fmt.Errorf("duplicate hydration table name %q", table.Name)
		}
		names[table.Name] = true
		if len(table.TagField) > 0 {
			if err := hydrationValidatePath(table.TagField); err != nil {
				return nil, err
			}
		}
		for _, generation := range table.Before {
			if len(generation.Field) > 0 || generation.State == "" {
				return nil, fmt.Errorf("hydrate %s: Before generation requires a state alias and no field", table.Name)
			}
			if err := validateGeneration(generation, sequenceNames); err != nil {
				return nil, err
			}
		}
		var paths [][]string
		for _, generation := range table.Generate {
			if err := validateGeneration(generation, sequenceNames); err != nil {
				return nil, fmt.Errorf("hydrate %s: %w", table.Name, err)
			}
			if len(generation.Field) > 0 {
				for _, path := range paths {
					if hydrationPathsOverlap(path, generation.Field) {
						return nil, fmt.Errorf("hydrate %s: generation fields overlap", table.Name)
					}
				}
				paths = append(paths, generation.Field)
			}
		}
		for _, capture := range table.Captures {
			if err := checkCapture(capture.Path, capture.Append); err != nil {
				return nil, err
			}
			if len(capture.Field) > 0 {
				if err := hydrationValidatePath(capture.Field); err != nil {
					return nil, err
				}
			}
		}
		for _, reference := range table.References {
			if reference.Table == "" || reference.Tag == "" {
				return nil, fmt.Errorf("hydrate %s: reference requires table and tag", table.Name)
			}
			if err := hydrationValidatePath(reference.Field); err != nil {
				return nil, err
			}
			for _, path := range paths {
				if hydrationPathsOverlap(path, reference.Field) {
					return nil, fmt.Errorf("hydrate %s: reference and generation fields overlap", table.Name)
				}
			}
			paths = append(paths, reference.Field)
			if len(reference.From) > 0 {
				if err := hydrationValidatePath(reference.From); err != nil {
					return nil, err
				}
			}
		}
		for _, path := range table.PostIncrement {
			if _, err := hydrationMutableStatePath(path); err != nil {
				return nil, err
			}
		}
		rows, err := e.resolver().Load(ctx, table.Source)
		if err != nil {
			return nil, fmt.Errorf("hydrate %s source: %w", table.Name, err)
		}
		if len(rows) > 1 {
			for _, capture := range table.Captures {
				if !capture.Append {
					return nil, fmt.Errorf("hydrate %s: capture %q requires Append for multiple rows", table.Name, capture.Path)
				}
			}
		}
		for i, row := range rows {
			rows[i] = hydrationContainers(row).(map[string]any)
		}
		count += len(rows)
		if count > maxRows {
			return nil, fmt.Errorf("hydration exceeds %d row limit", maxRows)
		}
		inputs[i] = hydrationInput{spec: table, rows: rows}
	}
	for _, input := range inputs {
		for _, reference := range input.spec.References {
			if !names[reference.Table] {
				return nil, fmt.Errorf("hydrate %s: reference table %q is not declared", input.spec.Name, reference.Table)
			}
		}
	}
	return inputs, nil
}
func validateGeneration(g Generation, sequences map[string]bool) error {
	if len(g.Field) == 0 && g.State == "" {
		return fmt.Errorf("generation requires a field or state alias")
	}
	if len(g.Field) > 0 {
		if err := hydrationValidatePath(g.Field); err != nil {
			return err
		}
	}
	if g.State != "" {
		if _, err := hydrationMutableStatePath(g.State); err != nil {
			return err
		}
	}
	variants := 0
	if g.Sequence != "" {
		variants++
		if !sequences[g.Sequence] {
			return fmt.Errorf("unknown fixture sequence %q", g.Sequence)
		}
	}
	if g.UUID {
		variants++
	}
	if g.HasValue || g.Value != nil {
		variants++
	}
	if variants != 1 {
		return fmt.Errorf("generation requires exactly one Sequence, UUID, or Value")
	}
	return nil
}
func (s *hydrationSession) generate(ctx context.Context, g Generation, row map[string]any) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(g.Field) > 0 && !g.Overwrite {
		existing, present, err := hydrationGet(row, g.Field)
		if err != nil {
			return false, err
		}
		if present {
			touched := false
			if g.State != "" {
				// Bind an existing authored template once before making its
				// concrete value available to later ordered generators.
				if !s.sourceExpanded {
					existing, err = s.expansion.expand(s.state, existing)
					if err != nil {
						return false, err
					}
					if err := hydrationSet(row, g.Field, existing); err != nil {
						return false, err
					}
					touched = true
				}
				if err := hydrationSet(map[string]any(s.state), strings.Split(g.State, "."), cloneValue(existing)); err != nil {
					return false, err
				}
			}
			return touched, nil
		}
	}
	var value any
	switch {
	case g.Sequence != "":
		sequence := s.sequences[g.Sequence]
		next, err := addFixtureInteger(sequence.Next, sequence.Step)
		if err != nil {
			return false, err
		}
		value = sequence.Next
		sequence.Next = next
		sequence.Allocated++
		s.sequences[g.Sequence] = sequence
	case g.UUID:
		generated, err := s.uuid()
		if err != nil {
			return false, err
		}
		if generated == "" {
			return false, fmt.Errorf("UUID generator returned empty text")
		}
		value = generated
	default:
		expanded, err := s.expansion.expand(s.state, g.Value)
		if err != nil {
			return false, err
		}
		value = expanded
	}
	if len(g.Field) > 0 {
		if err := hydrationSet(row, g.Field, cloneValue(value)); err != nil {
			return false, err
		}
	}
	if g.State != "" {
		if err := hydrationSet(map[string]any(s.state), strings.Split(g.State, "."), cloneValue(value)); err != nil {
			return false, err
		}
	}
	return true, nil
}
func (s *hydrationSession) resolve(ctx context.Context, row *hydrationRow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if row.status == 2 {
		return nil
	}
	if row.status == 1 {
		return fmt.Errorf("hydration reference cycle at %s tag %q row %d", row.table, row.tag, row.index+1)
	}
	row.status = 1
	for _, reference := range row.references {
		if !reference.spec.Overwrite {
			_, present, err := hydrationGet(row.row, reference.spec.Field)
			if err != nil {
				return err
			}
			if present {
				continue
			}
		}
		target := s.tags[reference.spec.Table][reference.tag]
		if target == nil {
			return fmt.Errorf("hydrate %s row %d: reference %s/%s is absent", row.table, row.index+1, reference.spec.Table, reference.tag)
		}
		needsResolution := len(reference.spec.From) == 0
		for _, pending := range target.references {
			if !hydrationPathsOverlap(reference.spec.From, pending.spec.Field) {
				continue
			}
			_, present, err := hydrationGet(target.row, pending.spec.Field)
			if err != nil {
				return err
			}
			if pending.spec.Overwrite || !present {
				needsResolution = true
			}
		}
		if needsResolution {
			if err := s.resolve(ctx, target); err != nil {
				return err
			}
		}
		value, present, err := hydrationGet(target.row, reference.spec.From)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("hydrate %s row %d: reference field is absent in %s/%s", row.table, row.index+1, reference.spec.Table, reference.tag)
		}
		copy, err := fixture.Clone([]map[string]any{{"value": value}})
		if err != nil {
			return err
		}
		if err := hydrationSet(row.row, reference.spec.Field, copy[0]["value"]); err != nil {
			return err
		}
	}
	row.status = 2
	return nil
}
func expandHydrationRecord(state data.Map, row map[string]any, skip [][]string) (map[string]any, error) {
	out := make(map[string]any, len(row))
	for _, key := range sortedKeys(row) {
		var children [][]string
		skipValue := false
		for _, path := range skip {
			if path[0] != key {
				continue
			}
			if len(path) == 1 {
				skipValue = true
				break
			}
			children = append(children, path[1:])
		}
		if skipValue {
			out[key] = cloneValue(row[key])
			continue
		}
		if len(children) > 0 {
			child, ok := row[key].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("generation parent %q is not an object", key)
			}
			value, err := expandHydrationRecord(state, child, children)
			if err != nil {
				return nil, err
			}
			out[key] = value
			continue
		}
		value, err := expandValue(state, row[key], false)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}
func hydrationValidatePath(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("field path is required")
	}
	for _, part := range path {
		if part == "" {
			return fmt.Errorf("field path contains an empty component")
		}
	}
	return nil
}
func hydrationStatePath(path string) ([]string, error) {
	if path == "" || strings.ContainsAny(path, "$[]{}()<>+") {
		return nil, fmt.Errorf("state path %q must be a literal dotted path", path)
	}
	parts := strings.Split(path, ".")
	if err := hydrationValidatePath(parts); err != nil {
		return nil, err
	}
	return parts, nil
}
func hydrationPathsOverlap(a, b []string) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func hydrationGet(row map[string]any, path []string) (any, bool, error) {
	var value any = row
	for _, part := range path {
		if value == nil {
			return nil, false, nil
		}
		v := reflect.ValueOf(value)
		for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return nil, false, nil
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return nil, false, fmt.Errorf("path component %q is not a text-keyed map", part)
			}
			entry := v.MapIndex(reflect.ValueOf(part).Convert(v.Type().Key()))
			if !entry.IsValid() {
				return nil, false, nil
			}
			value = entry.Interface()
		case reflect.Struct:
			found := false
			t := v.Type()
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				jsonName := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
				if (f.Name == part || jsonName == part) && f.IsExported() {
					value = v.Field(i).Interface()
					found = true
					break
				}
			}
			if !found {
				return nil, false, nil
			}
		default:
			return nil, false, fmt.Errorf("path component %q traverses %T", part, value)
		}
	}
	return value, true, nil
}
func hydrationSet(row map[string]any, path []string, value any) error {
	if err := hydrationValidatePath(path); err != nil {
		return err
	}
	node := row
	for _, part := range path[:len(path)-1] {
		existing, present := node[part]
		if !present {
			child := map[string]any{}
			node[part] = child
			node = child
			continue
		}
		switch child := existing.(type) {
		case map[string]any:
			node = child
		case data.Map:
			node = map[string]any(child)
		default:
			return fmt.Errorf("path %q parent %q is not an object", strings.Join(path, "."), part)
		}
	}
	node[path[len(path)-1]] = value
	return nil
}
func hydrationDelete(row map[string]any, path []string) error {
	node := row
	for _, part := range path[:len(path)-1] {
		child, ok := node[part].(map[string]any)
		if !ok {
			return fmt.Errorf("tag parent %q is not an object", part)
		}
		node = child
	}
	delete(node, path[len(path)-1])
	return nil
}
func captureHydrationValue(state data.Map, c HydrationCapture, value any) error {
	parts, err := hydrationStatePath(c.Path)
	if err != nil {
		return err
	}
	copy, err := fixture.Clone([]map[string]any{{"value": value}})
	if err != nil {
		return err
	}
	value = copy[0]["value"]
	existing, present, err := hydrationGet(map[string]any(state), parts)
	if err != nil {
		return err
	}
	if c.Append {
		var values []any
		if present {
			var ok bool
			values, ok = existing.([]any)
			if !ok {
				return fmt.Errorf("capture %q conflicts with non-append output", c.Path)
			}
		}
		values = append(values, value)
		return hydrationSet(map[string]any(state), parts, values)
	}
	if present {
		return fmt.Errorf("duplicate capture path %q; use Append explicitly", c.Path)
	}
	return hydrationSet(map[string]any(state), parts, value)
}

func hydrationMutableStatePath(path string) ([]string, error) {
	parts, err := hydrationStatePath(path)
	if err != nil {
		return nil, err
	}
	for _, reserved := range []string{"row", "table", "index", "tag", data.UDFKey} {
		if parts[0] == reserved {
			return nil, fmt.Errorf("state path %q uses reserved hydration context", path)
		}
	}
	return parts, nil
}
func hydrationContainers(value any) any {
	if value == nil {
		return nil
	}
	v := reflect.ValueOf(value)
	if v.Type() == reflect.TypeOf([]byte(nil)) {
		return cloneValue(value)
	}
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return value
		}
		if v.IsNil() {
			return map[string]any(nil)
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = hydrationContainers(iter.Value().Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return []any(nil)
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = hydrationContainers(v.Index(i).Interface())
		}
		return out
	default:
		return value
	}
}
