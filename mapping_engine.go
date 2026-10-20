package dbunit

import (
	"context"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/toolbox/data"
)

// MapRequest projects an explicitly selected logical fixture into physical tables.
// Source expressions are expanded once; mapping defaults are literal values.
type MapRequest struct {
	Mapping Mapping        `json:"mapping"`
	Source  fixture.Source `json:"source"`
	State   data.Map       `json:"state,omitempty"`
}

// MapResult contains concrete, detached datasets ready for Prepare. Mapping does
// not write to the database or reserve generated identifiers.
type MapResult struct {
	Datasets []Dataset `json:"datasets"`
}

func (e *Engine) Map(ctx context.Context, req MapRequest, options ...RunOption) (*MapResult, error) {
	config, err := configRun(options)
	if err != nil {
		return nil, err
	}
	plan, err := CompileMapping(req.Mapping)
	if err != nil {
		return nil, err
	}
	rows, err := e.resolver().Load(ctx, req.Source)
	if err != nil {
		return nil, err
	}
	state := e.runState(req.State, config)
	if !req.Source.Expanded {
		for i, row := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			value, err := expandValue(state, row, false)
			if err != nil {
				return nil, &OperationError{Operation: "map expansion", Dataset: req.Mapping.Name, Row: i + 1, Err: err}
			}
			rows[i] = value.(map[string]any)
		}
	}
	datasets, err := plan.Map(ctx, rows)
	if err != nil {
		return nil, err
	}
	return &MapResult{Datasets: datasets}, nil
}
