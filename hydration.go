package dbunit

import (
	"context"
	"fmt"

	"github.com/viant/dbunit/fixture"
	"github.com/viant/structology"
	"github.com/viant/toolbox/data"
)

// FixtureSequence is a local deterministic counter. Start includes zero; Step
// defaults to one. Seed replaces Start using one exact integer query result.
// A database seed is a snapshot, never a concurrent database ID reservation.
type FixtureSequence struct {
	Name  string `json:"name"`
	Start int64  `json:"start,omitempty"`
	// StartFrom is an explicit private-state path for continuation. It is not a template.
	StartFrom string        `json:"startFrom,omitempty"`
	Step      int64         `json:"step,omitempty"`
	Seed      *QueryRequest `json:"seed,omitempty"`
}
type FixtureSequenceState struct {
	Next      int64 `json:"next"`
	Step      int64 `json:"step"`
	Allocated int64 `json:"allocated"`
	Seeded    bool  `json:"seeded,omitempty"`
}
type SequenceRequest struct {
	State     data.Map          `json:"state,omitempty"`
	Sequences []FixtureSequence `json:"sequences"`
}
type SequenceResult struct {
	Sequences map[string]FixtureSequenceState `json:"sequences"`
}

// Generation selects exactly one of Sequence, UUID, or Value. HasValue permits
// an explicit nil Value. Field is an exact nested map path, never dotted SQL.
// An existing Field is retained without consuming a value unless Overwrite.
// State optionally names a private state alias available to following steps.
type Generation struct {
	Field     []string `json:"field,omitempty"`
	State     string   `json:"state,omitempty"`
	Sequence  string   `json:"sequence,omitempty"`
	UUID      bool     `json:"uuid,omitempty"`
	Value     any      `json:"value,omitempty"`
	HasValue  bool     `json:"hasValue,omitempty"`
	Overwrite bool     `json:"overwrite,omitempty"`
}

// RecordReference links tagged rows after all generation and template expansion.
// Forward references are allowed. References may depend on other references;
// cycles are rejected. From is an exact nested path; empty selects the whole row.
type RecordReference struct {
	Field     []string `json:"field"`
	Table     string   `json:"table"`
	Tag       string   `json:"tag"`
	From      []string `json:"from,omitempty"`
	Overwrite bool     `json:"overwrite,omitempty"`
}

// HydrationCapture publishes a final row or field under an explicit state path.
// Append collects ordered values; duplicate non-append destinations are errors.
type HydrationCapture struct {
	Field  []string `json:"field,omitempty"`
	Path   string   `json:"path"`
	Append bool     `json:"append,omitempty"`
}
type HydrationTable struct {
	Name   string         `json:"name,omitempty"`
	Table  Identifier     `json:"table,omitempty"`
	Source fixture.Source `json:"source"`
	// TagField explicitly selects and removes a tag field, e.g. ["!tag"].
	TagField []string `json:"tagField,omitempty"`
	// Before runs once, even for an empty source, and writes state aliases only.
	Before   []Generation `json:"before,omitempty"`
	Generate []Generation `json:"generate,omitempty"`
	// PostIncrement runs once per nonempty table entry after its rows expand.
	PostIncrement []string           `json:"postIncrement,omitempty"`
	References    []RecordReference  `json:"references,omitempty"`
	Captures      []HydrationCapture `json:"captures,omitempty"`
}
type HydrateRequest struct {
	State     data.Map          `json:"state,omitempty"`
	Sequences []FixtureSequence `json:"sequences,omitempty"`
	Tables    []HydrationTable  `json:"tables"`
	// UUID is an invocation-local test hook; nil uses crypto/rand UUID v4.
	UUID          func() (string, error)  `json:"-"`
	MaxRows       int                     `json:"maxRows,omitempty"`
	StateCaptures []HydrationStateCapture `json:"stateCaptures,omitempty"`
}
type HydrationStateCapture struct {
	From string `json:"from"`
	Path string `json:"path"`
}
type HydratedTable struct {
	Name   string         `json:"name"`
	Table  Identifier     `json:"table,omitempty"`
	Source fixture.Source `json:"source"`
}
type HydrateResult struct {
	Tables    []HydratedTable                      `json:"tables"`
	Sequences map[string]FixtureSequenceState      `json:"sequences"`
	Captures  data.Map                             `json:"captures"`
	Tags      map[string]map[string]map[string]any `json:"tags,omitempty"`
}

func (e *Engine) Sequence(ctx context.Context, req SequenceRequest, options ...RunOption) (*SequenceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := configRun(options)
	if err != nil {
		return nil, err
	}
	sequences, err := e.resolveFixtureSequences(ctx, req.Sequences, e.runState(req.State, c), c)
	if err != nil {
		return nil, err
	}
	return &SequenceResult{Sequences: sequences}, nil
}

func (e *Engine) Hydrate(ctx context.Context, req HydrateRequest, options ...RunOption) (*HydrateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := configRun(options)
	if err != nil {
		return nil, err
	}
	state := e.runState(req.State, c)
	inputs, err := e.hydrationInputs(ctx, req)
	if err != nil {
		return nil, err
	}
	sequences, err := e.resolveFixtureSequences(ctx, req.Sequences, state, c)
	if err != nil {
		return nil, err
	}
	session := &hydrationSession{state: state, sequences: sequences, uuid: req.UUID, tags: map[string]map[string]*hydrationRow{}}
	if session.uuid == nil {
		session.uuid = randomFixtureUUID
	}
	session.expansion = newHydrationExpansion(state)
	for i := range inputs {
		input := &inputs[i]
		session.sourceExpanded = input.spec.Source.Expanded
		for _, generation := range input.spec.Before {
			if _, err := session.generate(ctx, generation, nil); err != nil {
				return nil, fmt.Errorf("hydrate %s before: %w", input.spec.Name, err)
			}
		}
		for j, row := range input.rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			state["row"] = row
			state["table"] = input.spec.Name
			state["index"] = int64(j)
			state["tag"] = ""
			tag := ""
			if len(input.spec.TagField) > 0 {
				raw, present, err := hydrationGet(row, input.spec.TagField)
				if err != nil {
					return nil, err
				}
				if present {
					value := raw
					if !input.spec.Source.Expanded {
						value, err = session.expansion.expand(state, raw)
						if err != nil {
							return nil, err
						}
					}
					text, ok := value.(string)
					if !ok || text == "" {
						return nil, fmt.Errorf("hydrate %s row %d: tag must be nonempty text", input.spec.Name, j+1)
					}
					tag = text
					if err := hydrationDelete(row, input.spec.TagField); err != nil {
						return nil, err
					}
					state["tag"] = tag
				}
			}
			var generated [][]string
			for _, generation := range input.spec.Generate {
				applied, err := session.generate(ctx, generation, row)
				if err != nil {
					return nil, fmt.Errorf("hydrate %s row %d: %w", input.spec.Name, j+1, err)
				}
				if applied && len(generation.Field) > 0 {
					generated = append(generated, generation.Field)
				}
			}
			if !input.spec.Source.Expanded {
				session.expansion.failure = nil
				expanded, err := expandHydrationRecord(state, row, generated)
				if err == nil {
					err = session.expansion.failure
				}
				if err != nil {
					return nil, fmt.Errorf("hydrate %s row %d: %w", input.spec.Name, j+1, err)
				}
				row = expanded
			}
			state["row"] = row
			resolved := &hydrationRow{row: row, table: input.spec.Name, index: j, tag: tag}
			for _, reference := range input.spec.References {
				tagValue, err := session.expansion.expand(state, reference.Tag)
				if err != nil {
					return nil, err
				}
				referenceTag, ok := tagValue.(string)
				if !ok || referenceTag == "" {
					return nil, fmt.Errorf("hydrate %s row %d: reference tag must be nonempty text", input.spec.Name, j+1)
				}
				resolved.references = append(resolved.references, hydrationReference{spec: reference, tag: referenceTag})
			}
			input.resolved = append(input.resolved, resolved)
			if tag != "" {
				if session.tags[input.spec.Name] == nil {
					session.tags[input.spec.Name] = map[string]*hydrationRow{}
				}
				if session.tags[input.spec.Name][tag] != nil {
					return nil, fmt.Errorf("hydrate %s: duplicate tag %q", input.spec.Name, tag)
				}
				session.tags[input.spec.Name][tag] = resolved
			}
		}
		if len(input.rows) > 0 {
			for _, path := range input.spec.PostIncrement {
				if err := incrementFixtureState(state, path); err != nil {
					return nil, fmt.Errorf("hydrate %s postIncrement: %w", input.spec.Name, err)
				}
			}
		}
	}
	for _, input := range inputs {
		for _, row := range input.resolved {
			if err := session.resolve(ctx, row); err != nil {
				return nil, err
			}
		}
	}
	result := &HydrateResult{Sequences: sequences, Captures: data.Map{}, Tags: map[string]map[string]map[string]any{}}
	for _, input := range inputs {
		rows := make(structology.Maps, len(input.resolved))
		for i, resolved := range input.resolved {
			rows[i] = resolved.row
			for _, capture := range input.spec.Captures {
				value, present, err := hydrationGet(resolved.row, capture.Field)
				if err != nil {
					return nil, err
				}
				if !present {
					return nil, fmt.Errorf("hydrate %s row %d: capture field is absent", input.spec.Name, i+1)
				}
				if err := captureHydrationValue(result.Captures, capture, value); err != nil {
					return nil, err
				}
			}
			if resolved.tag != "" {
				if result.Tags[input.spec.Name] == nil {
					result.Tags[input.spec.Name] = map[string]map[string]any{}
				}
				result.Tags[input.spec.Name][resolved.tag] = cloneValue(resolved.row).(map[string]any)
			}
		}
		result.Tables = append(result.Tables, HydratedTable{Name: input.spec.Name, Table: append(Identifier(nil), input.spec.Table...), Source: fixture.Resolved(rows)})
	}
	for _, capture := range req.StateCaptures {
		parts, err := hydrationStatePath(capture.From)
		if err != nil {
			return nil, err
		}
		value, present, err := hydrationGet(map[string]any(state), parts)
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, fmt.Errorf("state capture source %q is absent", capture.From)
		}
		if err := captureHydrationValue(result.Captures, HydrationCapture{Path: capture.Path}, value); err != nil {
			return nil, err
		}
	}
	return result, nil
}
