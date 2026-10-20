package dbunit

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/viant/toolbox/data"
)

func randomFixtureUUID() (string, error) {
	value, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}
func (e *Engine) resolveFixtureSequences(ctx context.Context, specs []FixtureSequence, state data.Map, c runConfig) (map[string]FixtureSequenceState, error) {
	result := map[string]FixtureSequenceState{}
	for _, spec := range specs {
		if spec.Name == "" {
			return nil, fmt.Errorf("fixture sequence name is required")
		}
		if _, present := result[spec.Name]; present {
			return nil, fmt.Errorf("duplicate fixture sequence %q", spec.Name)
		}
		if spec.Seed != nil && spec.StartFrom != "" {
			return nil, fmt.Errorf("fixture sequence %s: Seed and StartFrom are mutually exclusive", spec.Name)
		}
		if spec.StartFrom != "" {
			if _, err := hydrationStatePath(spec.StartFrom); err != nil {
				return nil, err
			}
		}
		step := spec.Step
		if step == 0 {
			step = 1
		}
		result[spec.Name] = FixtureSequenceState{Next: spec.Start, Step: step}
	}
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sequence := result[spec.Name]
		if spec.StartFrom != "" {
			value, present, err := hydrationGet(map[string]any(state), strings.Split(spec.StartFrom, "."))
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, fmt.Errorf("fixture sequence %s: StartFrom %q is absent", spec.Name, spec.StartFrom)
			}
			next, err := fixtureInteger(value)
			if err != nil {
				return nil, fmt.Errorf("fixture sequence %s: %w", spec.Name, err)
			}
			sequence.Next = next
		}
		if spec.Seed != nil {
			query := spec.Seed
			ex, _, err := e.executor(query.Database, c)
			if err != nil {
				return nil, err
			}
			seedState := cloneMap(state)
			for k, v := range query.State {
				seedState[k] = cloneValue(v)
			}
			for k, v := range c.state {
				seedState[k] = cloneValue(v)
			}
			registerUDFs(seedState)
			seedExpansion := newHydrationExpansion(seedState)
			args := make([]any, len(query.Args))
			for i, value := range query.Args {
				args[i], err = seedExpansion.expand(seedState, value)
				if err != nil {
					return nil, fmt.Errorf("fixture sequence %s seed: %w", spec.Name, err)
				}
			}
			rows, err := e.query(ctx, ex, query.SQL, args, 2)
			if err != nil {
				return nil, fmt.Errorf("fixture sequence %s seed: %w", spec.Name, err)
			}
			if len(rows.Rows) != 1 || len(rows.Columns) != 1 {
				return nil, fmt.Errorf("fixture sequence %s seed requires exactly one row and one column", spec.Name)
			}
			next, err := fixtureInteger(rows.Rows[0][rows.Columns[0]])
			if err != nil {
				return nil, fmt.Errorf("fixture sequence %s seed: %w", spec.Name, err)
			}
			sequence.Next = next
			sequence.Seeded = true
		}
		result[spec.Name] = sequence
	}
	return result, nil
}
func fixtureInteger(value any) (int64, error) {
	number, ok := number(value)
	if !ok || !number.IsInt() || !number.Num().IsInt64() {
		return 0, fmt.Errorf("expected an exact signed 64-bit integer, got %v (%T)", value, value)
	}
	return number.Num().Int64(), nil
}
func addFixtureInteger(value, step int64) (int64, error) {
	if step > 0 && value > math.MaxInt64-step || step < 0 && value < math.MinInt64-step {
		return 0, fmt.Errorf("fixture sequence overflow: %d plus %d", value, step)
	}
	return value + step, nil
}
func incrementFixtureState(state data.Map, path string) error {
	parts, err := hydrationMutableStatePath(path)
	if err != nil {
		return err
	}
	raw, present, err := hydrationGet(map[string]any(state), parts)
	if err != nil {
		return err
	}
	var value int64
	if present {
		value, err = fixtureInteger(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	next, err := addFixtureInteger(value, 1)
	if err != nil {
		return err
	}
	return hydrationSet(map[string]any(state), parts, next)
}
