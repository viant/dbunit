package dbunit

import (
	"fmt"
	"github.com/viant/assertly"
	"github.com/viant/toolbox"
	"github.com/viant/toolbox/data"
	"github.com/viant/toolbox/data/udf"
	"sort"
	"strings"
)

func newAssertionContext() *assertly.Context {
	r := toolbox.NewValueProviderRegistry()
	for name, provider := range map[string]toolbox.ValueProvider{"nil": toolbox.NewNilValueProvider(), "empty": toolbox.NewConstValueProvider(""), "env": toolbox.NewEnvValueProvider(), "cast": toolbox.NewCastedValueProvider(), "timediff": toolbox.NewTimeDiffProvider(), "current_timestamp": toolbox.NewCurrentTimeProvider(), "current_date": toolbox.NewCurrentDateProvider(), "between": toolbox.NewBetweenPredicateValueProvider(), "within_sec": toolbox.NewWithinSecPredicateValueProvider(), "weekday": toolbox.NewWeekdayProvider(), "dob": toolbox.NewDateOfBirthrovider()} {
		r.Register(name, provider)
	}
	return assertly.NewContext(nil, nil, toolbox.NewMacroEvaluator("<ds:", ">", r))
}
func expandValue(state data.Map, value any, expect bool) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			expanded, err := expandValue(state, v[k], expect)
			if err != nil {
				return nil, err
			}
			out[k] = expanded
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			expanded, err := expandValue(state, item, expect)
			if err != nil {
				return nil, err
			}
			out[i] = expanded
		}
		return out, nil
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			expanded, err := expandValue(state, item, expect)
			if err != nil {
				return nil, err
			}
			out[i] = expanded.(map[string]any)
		}
		return out, nil
	case string:
		expanded := state.Expand(v)
		text, ok := expanded.(string)
		if !ok {
			return expanded, nil
		}
		if !expect && strings.Contains(text, "<ds:") {
			ctx := newAssertionContext()
			result, err := ctx.Evaluator.Expand(ctx.Context, text)
			if err != nil {
				return nil, err
			}
			if _, ok := result.(toolbox.Predicate); ok {
				return nil, fmt.Errorf("predicate cannot be used as prepared column value")
			}
			return result, nil
		}
		return expanded, nil
	default:
		return cloneValue(value), nil
	}
}
func concrete(value any) bool {
	switch v := value.(type) {
	case string:
		return !strings.Contains(v, "<ds:")
	case toolbox.Predicate:
		return false
	default:
		return true
	}
}

// registerUDFs installs a private registry for each run. Explicit caller UDFs
// override the defaults, without mutating toolbox's global predefined registry.
func registerUDFs(state data.Map) {
	registry := data.Map{}
	for name, fn := range udf.Predefined {
		registry[name] = fn
	}
	switch custom := state[data.UDFKey].(type) {
	case data.Map:
		for name, fn := range custom {
			registry[name] = fn
		}
	case map[string]any:
		for name, fn := range custom {
			registry[name] = fn
		}
	}
	// Toolbox also permits UDFs directly in state. Preserve that convention.
	for name, value := range state {
		if _, ok := value.(func(any, data.Map) (any, error)); ok {
			registry[name] = value
		}
	}
	state[data.UDFKey] = registry
}
