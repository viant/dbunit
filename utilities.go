package dbunit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/viant/structology"
)

// FreezeRequest captures an explicit bounded query as a reusable fixture.
// Persist Content to an explicit destination chosen by the caller.
type FreezeRequest struct {
	Query  QueryRequest `json:"query"`
	Format string       `json:"format,omitempty"`
}
type FrozenFixture struct {
	Format  string `json:"format"`
	Content []byte `json:"content"`
	Rows    int    `json:"rows"`
}

func (e *Engine) Freeze(ctx context.Context, req FreezeRequest, options ...RunOption) (*FrozenFixture, error) {
	format := strings.ToLower(req.Format)
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "ndjson" {
		return nil, fmt.Errorf("freeze format must be json or ndjson")
	}
	result, err := e.Query(ctx, req.Query, options...)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if format == "json" {
		encoder.SetIndent("", "  ")
		err = encoder.Encode(result.Rows)
	} else {
		for _, row := range result.Rows {
			if err = encoder.Encode(row); err != nil {
				break
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return &FrozenFixture{Format: format, Content: buffer.Bytes(), Rows: len(result.Rows)}, nil
}

// CompareRequest compares two explicit query results, including named databases.
// The caller supplies deterministic ORDER BY clauses when Ordered is enabled.
type CompareRequest struct {
	Name     string       `json:"name,omitempty"`
	Expected QueryRequest `json:"expected"`
	Actual   QueryRequest `json:"actual"`
	Match    MatchOptions `json:"match"`
}

func (e *Engine) Compare(ctx context.Context, req CompareRequest, options ...RunOption) (*Report, error) {
	if req.Match.MaxRows > 0 {
		for _, query := range []*QueryRequest{&req.Expected, &req.Actual} {
			if query.MaxRows <= 0 || query.MaxRows > req.Match.MaxRows {
				query.MaxRows = req.Match.MaxRows
			}
		}
	}
	expected, err := e.Query(ctx, req.Expected, options...)
	if err != nil {
		return nil, err
	}
	actual, err := e.Query(ctx, req.Actual, options...)
	if err != nil {
		return nil, err
	}
	if len(expected.Rows) == 0 && !req.Match.AllowEmpty {
		return nil, fmt.Errorf("empty comparison requires AllowEmpty")
	}
	if req.Match.Ordered && len(req.Match.Keys) > 0 {
		return nil, fmt.Errorf("ordered and keyed matching are mutually exclusive")
	}
	if req.Match.Ordered && (!hasOrderBy(req.Expected.SQL) || !hasOrderBy(req.Actual.SQL)) {
		return nil, fmt.Errorf("ordered comparison requires ORDER BY in both queries")
	}
	if !req.Match.Ordered && len(req.Match.Keys) == 0 && !req.Match.Multiset && len(expected.Rows) > 1 {
		return nil, fmt.Errorf("unordered comparison requires keys or Multiset")
	}
	if err := decodeCompareJSON(expected.Rows, req.Match.JSONColumns); err != nil {
		return nil, err
	}
	if err := decodeCompareJSON(actual.Rows, req.Match.JSONColumns); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	if len(req.Match.Keys) > 0 {
		for _, row := range expected.Rows {
			key, err := rowKey(row, req.Match.Keys)
			if err != nil {
				return nil, err
			}
			if seen[key] {
				return nil, fmt.Errorf("duplicate expected key")
			}
			seen[key] = true
		}
	}
	report := &Report{Name: req.Name}
	compareRows(Expectation{Name: req.Name, Match: req.Match}, expected.Rows, actual.Rows, report)
	if report.Failed > 0 {
		report.failure = &AssertionError{Report: report}
		return report, report.failure
	}
	return report, nil
}

func decodeCompareJSON(rows structology.Maps, columns []string) error {
	for _, row := range rows {
		for _, column := range columns {
			value, ok := row[column]
			if !ok || value == nil {
				continue
			}
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("JSON column %q is not textual", column)
			}
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.UseNumber()
			var decoded any
			if err := decoder.Decode(&decoded); err != nil {
				return fmt.Errorf("JSON column %q: %w", column, err)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return fmt.Errorf("JSON column %q contains trailing data", column)
			}
			row[column] = decoded
		}
	}
	return nil
}
