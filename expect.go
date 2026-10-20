package dbunit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/viant/assertly"
	"github.com/viant/structology"
	"github.com/viant/toolbox"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) Expect(ctx context.Context, req ExpectRequest, options ...RunOption) (*Report, error) {
	started := time.Now()
	c, err := configRun(options)
	if err != nil {
		return nil, err
	}
	state := e.runState(req.State, c)
	report := &Report{}
	for _, spec := range req.Expectations {
		expected, err := e.resolver().Load(ctx, spec.Expected)
		if err != nil {
			report.failure = &OperationError{Operation: "expect source", Dataset: spec.Name, Err: err}
			return report, report.failure
		}
		for i, row := range expected {
			if spec.Expected.Expanded {
				continue
			}
			expanded, err := expandValue(state, row, true)
			if err != nil {
				report.failure = err
				return report, err
			}
			expected[i] = expanded.(map[string]any)
		}
		if spec.Query != nil {
			q := *spec.Query
			q.Args = make([]any, len(spec.Query.Args))
			for i, v := range spec.Query.Args {
				q.Args[i], err = expandValue(state, v, false)
				if err != nil {
					return report, err
				}
			}
			spec.Query = &q
		}
		if err = e.expect(ctx, c, spec, expected, report); err != nil {
			report.failure = &OperationError{Operation: "expect", Dataset: spec.Name, Err: err}
			return report, report.failure
		}
	}
	report.Duration = time.Since(started)
	if report.Failed > 0 {
		report.failure = &AssertionError{Report: report}
	}
	return report, report.failure
}
func (e *Engine) expect(ctx context.Context, c runConfig, spec Expectation, expected structology.Maps, report *Report) error {
	scope := spec.Scope
	if scope == "" {
		scope = Full
	}
	if scope != Full && scope != Keys && scope != Query {
		return fmt.Errorf("unknown scope %q", scope)
	}
	if (len(spec.Table) == 0) == (spec.Query == nil) {
		return fmt.Errorf("specify exactly one table or query")
	}
	if scope == Query && spec.Query == nil {
		return fmt.Errorf("query scope requires query")
	}
	if scope == Keys && (spec.Query != nil || len(spec.Match.Keys) == 0) {
		return fmt.Errorf("keys scope requires table and concrete keys")
	}
	if scope == Keys && len(expected) == 0 && !spec.Match.AllowEmpty {
		return fmt.Errorf("empty keys expectation is vacuous; enable AllowEmpty explicitly")
	}
	if spec.Match.Ordered && len(spec.Match.Keys) > 0 {
		return fmt.Errorf("ordered and keyed matching are mutually exclusive")
	}
	if spec.Match.Ordered && spec.Query == nil && len(spec.Match.OrderBy) == 0 {
		return fmt.Errorf("ordered table comparison requires OrderBy")
	}
	if spec.Match.Ordered && spec.Query != nil && !hasOrderBy(spec.Query.SQL) {
		return fmt.Errorf("ordered query comparison requires explicit ORDER BY")
	}
	if !spec.Match.Ordered && len(spec.Match.Keys) == 0 && !spec.Match.Multiset && len(expected) > 1 {
		return fmt.Errorf("unordered comparison requires keys or Multiset")
	}
	if len(spec.Match.Keys) > 0 {
		seen := map[string]bool{}
		for _, row := range expected {
			k, err := rowKey(row, spec.Match.Keys)
			if err != nil {
				return err
			}
			if seen[k] {
				return fmt.Errorf("duplicate expected key")
			}
			seen[k] = true
		}
	}
	if spec.Query == nil {
		if _, err := e.generatedDatabase(spec.Database); err != nil {
			return err
		}
	}
	ex, _, err := e.executor(spec.Database, c)
	if err != nil {
		return err
	}
	actual := structology.Maps{}
	max := spec.Match.MaxRows
	if max <= 0 {
		max = e.maxRows
	}
	if len(expected) > max {
		return fmt.Errorf("expected rows exceed %d row limit", max)
	}
	if spec.Query != nil {
		q, err := e.query(ctx, ex, spec.Query.SQL, spec.Query.Args, max)
		if err != nil {
			return err
		}
		actual = q.Rows
	} else {
		table, err := e.quote(spec.Table)
		if err != nil {
			return err
		}
		sqlText := "SELECT * FROM " + table
		if scope == Keys {
			batchRows := 100
			if dialect, err := e.sqlxDialect(); err == nil {
				if n := dialect.MaxPlaceholderCount() / len(spec.Match.Keys); n < batchRows {
					batchRows = n
				}
			}
			if batchRows < 1 {
				return fmt.Errorf("key tuple exceeds bind limit")
			}
			for start := 0; start < len(expected); {
				end := start + batchRows
				if end > len(expected) {
					end = len(expected)
				}
				var predicates []string
				var args []any
				for _, row := range expected[start:end] {
					pred, bound, err := e.keyPredicate(spec.Match.Keys, row, len(args))
					if err != nil {
						return err
					}
					predicates = append(predicates, "("+pred+")")
					args = append(args, bound...)
				}
				remaining := max - len(actual)
				if remaining < 1 {
					remaining = 1
				}
				q, err := e.query(ctx, ex, sqlText+" WHERE "+strings.Join(predicates, " OR "), args, remaining)
				if err != nil {
					return err
				}
				actual = append(actual, q.Rows...)
				if len(actual) > max {
					return fmt.Errorf("selected rows exceed %d row limit", max)
				}
				start = end
			}
		} else {
			if len(spec.Match.OrderBy) > 0 {
				var cols []string
				for _, col := range spec.Match.OrderBy {
					quoted, err := e.quote(Table(col))
					if err != nil {
						return err
					}
					cols = append(cols, quoted)
				}
				sqlText += " ORDER BY " + strings.Join(cols, ",")
			}
			q, err := e.query(ctx, ex, sqlText, nil, max)
			if err != nil {
				return err
			}
			actual = q.Rows
		}
	}
	if err := decodeCompareJSON(actual, spec.Match.JSONColumns); err != nil {
		return err
	}
	compareRows(spec, expected, actual, report)
	return nil
}
func addMismatch(r *Report, spec Expectation, path, category string, expected, actual any) {
	r.Failed++
	if len(r.Mismatches) < 100 {
		r.Mismatches = append(r.Mismatches, Mismatch{Expectation: spec.Name, Path: path, Category: category, Expected: expected, Actual: actual})
	}
}
func compareRows(spec Expectation, expected, actual structology.Maps, r *Report) {
	if (!spec.Match.Contains && len(expected) != len(actual)) || (spec.Match.Contains && len(expected) > len(actual)) {
		addMismatch(r, spec, "rows", "cardinality", len(expected), len(actual))
	} else {
		r.Passed++
	}
	ctx := newAssertionContext()
	if len(spec.Match.Keys) > 0 {
		actualByKey := map[string]map[string]any{}
		for i, row := range actual {
			k, err := rowKey(row, spec.Match.Keys)
			if err != nil {
				addMismatch(r, spec, fmt.Sprintf("rows[%d]", i), "key", nil, err.Error())
				continue
			}
			if _, ok := actualByKey[k]; ok {
				addMismatch(r, spec, fmt.Sprintf("rows[%d]", i), "duplicateKey", nil, k)
			}
			actualByKey[k] = row
		}
		for i, row := range expected {
			k, _ := rowKey(row, spec.Match.Keys)
			got, ok := actualByKey[k]
			if !ok {
				addMismatch(r, spec, fmt.Sprintf("rows[%d]", i), "missingRow", row, nil)
				continue
			}
			compareReport(r, spec, fmt.Sprintf("rows[%d]", i), row, got, ctx)
		}
		return
	}
	if spec.Match.Ordered {
		for i, row := range expected {
			if i >= len(actual) {
				addMismatch(r, spec, fmt.Sprintf("rows[%d]", i), "missingRow", row, nil)
				continue
			}
			compareReport(r, spec, fmt.Sprintf("rows[%d]", i), row, actual[i], ctx)
		}
		return
	}
	if len(expected) > 256 || len(actual) > 256 {
		addMismatch(r, spec, "rows", "matchingLimit", "use keys or ordered matching for more than 256 rows", len(actual))
		return
	}
	edges := make([][]int, len(expected))
	var singleFailures []Mismatch
	for i, row := range expected {
		for j, got := range actual {
			failures := compareValue(fmt.Sprintf("rows[%d]", i), row, got, spec.Match.StrictFields, ctx)
			if len(expected) == 1 && len(actual) == 1 {
				singleFailures = failures
			}
			if len(failures) == 0 {
				edges[i] = append(edges[i], j)
			}
		}
	}
	owners := make([]int, len(actual))
	for i := range owners {
		owners[i] = -1
	}
	var augment func(int, []bool) bool
	augment = func(i int, seen []bool) bool {
		for _, j := range edges[i] {
			if seen[j] {
				continue
			}
			seen[j] = true
			if owners[j] < 0 || augment(owners[j], seen) {
				owners[j] = i
				return true
			}
		}
		return false
	}
	for i, row := range expected {
		if augment(i, make([]bool, len(actual))) {
			r.Passed++
		} else {
			if len(expected) == 1 && len(actual) == 1 {
				for _, failure := range singleFailures {
					addMismatch(r, spec, failure.Path, failure.Category, failure.Expected, failure.Actual)
				}
			} else {
				addMismatch(r, spec, fmt.Sprintf("rows[%d]", i), "missingRow", row, nil)
			}
		}
	}
}
func compareReport(r *Report, spec Expectation, path string, expected, actual any, ctx *assertly.Context) {
	failures := compareValue(path, expected, actual, spec.Match.StrictFields, ctx)
	if len(failures) == 0 {
		r.Passed++
		return
	}
	for _, f := range failures {
		addMismatch(r, spec, f.Path, f.Category, f.Expected, f.Actual)
	}
}
func compareValue(path string, expected, actual any, strict bool, ctx *assertly.Context) []Mismatch {
	bad := func(category string) []Mismatch {
		return []Mismatch{{Path: path, Category: category, Expected: expected, Actual: actual}}
	}
	if _, ok := expected.(toolbox.Predicate); ok {
		return assertValue(path, expected, actual, ctx)
	}
	if text, ok := expected.(string); ok && strings.Contains(text, "<ds:") && !strings.HasPrefix(text, `\<ds:`) {
		return assertValue(path, expected, actual, ctx)
	}
	if expected == nil || actual == nil {
		if expected == nil && actual == nil {
			return nil
		}
		return bad("value")
	}
	if text, ok := expected.(string); ok && strings.HasPrefix(text, `\<ds:`) {
		if got, ok := actual.(string); ok && got == strings.TrimPrefix(text, `\`) {
			return nil
		}
		return bad("value")
	}
	if _, ok := expected.(toolbox.Predicate); ok {
		return assertValue(path, expected, actual, ctx)
	}
	if text, ok := expected.(string); ok && strings.Contains(text, "<ds:") {
		return assertValue(path, expected, actual, ctx)
	}
	if got, ok := actual.(time.Time); ok {
		want, isTime := expected.(time.Time)
		if text, isText := expected.(string); isText {
			var err error
			want, err = parseSQLTime(text)
			isTime = err == nil
		}
		if isTime && got.Equal(want) {
			return nil
		}
		return bad("time")
	}
	if want, ok := expected.(time.Time); ok {
		if text, isText := actual.(string); isText {
			got, err := parseSQLTime(text)
			if err == nil && want.Equal(got) {
				return nil
			}
		}
		return bad("time")
	}
	if got, ok := actual.([]byte); ok {
		if text, isText := expected.(string); isText {
			decoded, err := base64.StdEncoding.DecodeString(text)
			if err == nil && bytes.Equal(decoded, got) {
				return nil
			}
			return bad("binary")
		}
	}
	if e, ok := number(expected); ok {
		a, ok := number(actual)
		if ok && e.Cmp(a) == 0 {
			return nil
		}
		return bad("number")
	}
	if e, ok := expected.(map[string]any); ok {
		a, ok := actual.(map[string]any)
		if !ok {
			return bad("type")
		}
		var failures []Mismatch
		keys := sortedKeys(e)
		for _, k := range keys {
			if strings.HasPrefix(k, "@") {
				failures = append(failures, Mismatch{Path: path + "." + k, Category: "directive", Expected: e[k], Actual: "unsupported directive"})
				continue
			}
			value, present := a[k]
			if !present {
				failures = append(failures, Mismatch{Path: path + "." + k, Category: "missingField", Expected: e[k]})
				continue
			}
			failures = append(failures, compareValue(path+"."+k, e[k], value, strict, ctx)...)
		}
		if strict {
			for _, k := range sortedKeys(a) {
				if _, ok := e[k]; !ok {
					failures = append(failures, Mismatch{Path: path + "." + k, Category: "unexpectedField", Actual: a[k]})
				}
			}
		}
		return failures
	}
	ev, av := reflect.ValueOf(expected), reflect.ValueOf(actual)
	if ev.Kind() == reflect.Slice || ev.Kind() == reflect.Array {
		if av.Kind() != reflect.Slice && av.Kind() != reflect.Array {
			return bad("type")
		}
		if ev.Kind() == reflect.Slice && av.Kind() == reflect.Slice && ev.IsNil() != av.IsNil() {
			return bad("value")
		}
		if ev.Len() != av.Len() {
			return bad("cardinality")
		}
		var failures []Mismatch
		for i := 0; i < ev.Len(); i++ {
			failures = append(failures, compareValue(fmt.Sprintf("%s[%d]", path, i), ev.Index(i).Interface(), av.Index(i).Interface(), strict, ctx)...)
		}
		return failures
	}
	if reflect.DeepEqual(expected, actual) {
		return nil
	}
	return bad("value")
}
func assertValue(path string, expected, actual any, ctx *assertly.Context) []Mismatch {
	validation, err := assertly.AssertWithContext(expected, actual, assertly.NewDataPath(path), ctx)
	if err != nil {
		return []Mismatch{{Path: path, Category: "predicate", Expected: expected, Actual: err.Error()}}
	}
	if validation.FailedCount == 0 {
		return nil
	}
	r := make([]Mismatch, 0, len(validation.Failures))
	for _, f := range validation.Failures {
		r = append(r, Mismatch{Path: f.Path, Category: "predicate", Expected: f.Expected, Actual: f.Actual, Message: f.Message})
	}
	return r
}
func number(v any) (*big.Rat, bool) {
	var text string
	switch n := v.(type) {
	case json.Number:
		text = string(n)
	case int:
		text = strconv.FormatInt(int64(n), 10)
	case int8:
		text = strconv.FormatInt(int64(n), 10)
	case int16:
		text = strconv.FormatInt(int64(n), 10)
	case int32:
		text = strconv.FormatInt(int64(n), 10)
	case int64:
		text = strconv.FormatInt(n, 10)
	case uint:
		text = strconv.FormatUint(uint64(n), 10)
	case uint8:
		text = strconv.FormatUint(uint64(n), 10)
	case uint16:
		text = strconv.FormatUint(uint64(n), 10)
	case uint32:
		text = strconv.FormatUint(uint64(n), 10)
	case uint64:
		text = strconv.FormatUint(n, 10)
	case float32:
		text = strconv.FormatFloat(float64(n), 'g', -1, 32)
	case float64:
		text = strconv.FormatFloat(n, 'g', -1, 64)
	default:
		return nil, false
	}
	r, ok := new(big.Rat).SetString(text)
	return r, ok
}
func rowKey(row map[string]any, keys []string) (string, error) {
	var b strings.Builder
	for _, key := range keys {
		value, present := row[key]
		if !present || !concrete(value) {
			return "", fmt.Errorf("key %q is absent or not concrete", key)
		}
		var token string
		if n, ok := number(value); ok {
			token = "n:" + n.RatString()
		} else {
			switch v := value.(type) {
			case nil:
				token = "nil"
			case string:
				token = "s:" + v
			case bool:
				token = fmt.Sprint("b:", v)
			case time.Time:
				token = "t:" + v.UTC().Format(time.RFC3339Nano)
			default:
				return "", fmt.Errorf("key %q type %T is unsupported", key, value)
			}
		}
		fmt.Fprintf(&b, "%d:%s", len(token), token)
	}
	return b.String(), nil
}

// SortMismatches provides deterministic presentation for consumers merging reports.
func SortMismatches(m []Mismatch) {
	sort.SliceStable(m, func(i, j int) bool { return m[i].Path < m[j].Path })
}
