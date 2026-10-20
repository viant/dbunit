package dbunit

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/structology"
	"strconv"
	"strings"
)

func (e *Engine) Query(ctx context.Context, req QueryRequest, options ...RunOption) (*QueryResult, error) {
	c, err := configRun(options)
	if err != nil {
		return nil, err
	}
	ex, _, err := e.executor(req.Database, c)
	if err != nil {
		return nil, err
	}
	state := e.runState(req.State, c)
	args := make([]any, len(req.Args))
	for i, v := range req.Args {
		args[i], err = expandValue(state, v, false)
		if err != nil {
			return nil, err
		}
	}
	return e.query(ctx, ex, req.SQL, args, req.MaxRows)
}
func (e *Engine) query(ctx context.Context, ex Executor, query string, args []any, maxRows int) (*QueryResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query SQL is required")
	}
	if maxRows <= 0 {
		maxRows = e.maxRows
	}
	rows, err := ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, c := range columns {
		if seen[c] {
			return nil, fmt.Errorf("duplicate query column %q: supply distinct aliases", c)
		}
		seen[c] = true
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	result := &QueryResult{Columns: columns, Rows: structology.Maps{}}
	for rows.Next() {
		if len(result.Rows) >= maxRows {
			return nil, fmt.Errorf("query exceeds %d row limit", maxRows)
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		record := make(map[string]any, len(columns))
		for i, col := range columns {
			v := values[i]
			if b, ok := v.([]byte); ok {
				typ := strings.ToUpper(types[i].DatabaseTypeName())
				if strings.Contains(typ, "BLOB") || strings.Contains(typ, "BINARY") || typ == "BYTEA" {
					v = append([]byte(nil), b...)
				} else {
					v = string(b)
				}
			}
			typ := strings.ToUpper(types[i].DatabaseTypeName())
			if text, ok := v.(string); ok {
				switch {
				case strings.Contains(typ, "INT"):
					n, parseErr := strconv.ParseInt(text, 10, 64)
					if parseErr == nil {
						v = n
					} else {
						u, uerr := strconv.ParseUint(text, 10, 64)
						if uerr != nil {
							return nil, fmt.Errorf("invalid integer column %q", col)
						}
						v = u
					}
				case strings.Contains(typ, "FLOAT") || strings.Contains(typ, "DOUBLE") || strings.Contains(typ, "REAL"):
					f, parseErr := strconv.ParseFloat(text, 64)
					if parseErr != nil {
						return nil, fmt.Errorf("invalid float column %q", col)
					}
					v = f
				}
			}
			if strings.Contains(typ, "BOOL") {
				if n, ok := v.(int64); ok {
					if n != 0 && n != 1 {
						return nil, fmt.Errorf("invalid boolean column %q", col)
					}
					v = n == 1
				}
			}
			if text, ok := v.(string); ok && (strings.Contains(typ, "DECIMAL") || strings.Contains(typ, "NUMERIC")) {
				if _, valid := number(json.Number(text)); !valid {
					return nil, fmt.Errorf("invalid numeric column %q", col)
				}
				v = json.Number(text)
			}
			record[col] = v
		}
		result.Rows = append(result.Rows, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
