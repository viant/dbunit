package dbunit

import (
	"encoding/base64"
	"fmt"
	"github.com/viant/assertly"
	"github.com/viant/structology"
	"github.com/viant/toolbox"
	"sort"
	"strings"
	"time"
)

// An index supplies candidates, never a comparison result. compareValue remains
// authoritative, including partial fields, predicates and numeric/binary/time
// equivalence. Values the index cannot classify remain candidates for every key.
type scalarMatchKey struct {
	kind  byte
	value string
}

type rowCandidates struct {
	rows []int
	free int // assigned rows never become unassigned during augmentation
}

type fieldCandidates struct {
	values    map[scalarMatchKey][]int
	unindexed []int
	lookups   map[string]*rowCandidates
}

type unorderedCandidates struct {
	actual structology.Maps
	fields map[string]*fieldCandidates
	all    *rowCandidates
}

func newUnorderedCandidates(actual structology.Maps) *unorderedCandidates {
	all := make([]int, len(actual))
	for i := range all {
		all[i] = i
	}
	return &unorderedCandidates{actual: actual, fields: map[string]*fieldCandidates{}, all: &rowCandidates{rows: all}}
}

func (c *unorderedCandidates) forRow(row map[string]any) *rowCandidates {
	best := c.all
	for _, field := range sortedKeys(row) {
		keys := scalarMatchKeys(row[field], true)
		if len(keys) == 0 {
			continue
		}
		index := c.fields[field]
		if index == nil {
			index = &fieldCandidates{values: map[scalarMatchKey][]int{}, lookups: map[string]*rowCandidates{}}
			for j, actual := range c.actual {
				value, present := actual[field]
				if !present {
					continue
				}
				actualKeys := scalarMatchKeys(value, false)
				if len(actualKeys) == 0 {
					index.unindexed = append(index.unindexed, j)
				}
				for _, key := range actualKeys {
					index.values[key] = append(index.values[key], j)
				}
			}
			c.fields[field] = index
		}
		var token strings.Builder
		for _, key := range keys {
			token.WriteByte(key.kind)
			fmt.Fprintf(&token, "%d:%s", len(key.value), key.value)
		}
		lookup := token.String()
		candidates := index.lookups[lookup]
		if candidates == nil {
			rows := append([]int(nil), index.unindexed...)
			for _, key := range keys {
				rows = append(rows, index.values[key]...)
			}
			sort.Ints(rows)
			unique := rows[:0]
			for _, j := range rows {
				if len(unique) == 0 || unique[len(unique)-1] != j {
					unique = append(unique, j)
				}
			}
			candidates = &rowCandidates{rows: unique}
			index.lookups[lookup] = candidates
		}
		if len(candidates.rows) < len(best.rows) {
			best = candidates
		}
		if len(best.rows) <= 1 {
			break
		}
	}
	return best
}

func scalarMatchKeys(value any, expected bool) []scalarMatchKey {
	if expected {
		if _, predicate := value.(toolbox.Predicate); predicate {
			return nil
		}
	}
	if value == nil {
		return []scalarMatchKey{{kind: '0'}}
	}
	if n, ok := number(value); ok {
		return []scalarMatchKey{{kind: 'n', value: n.RatString()}}
	}
	switch v := value.(type) {
	case bool:
		return []scalarMatchKey{{kind: 'b', value: fmt.Sprint(v)}}
	case time.Time:
		return []scalarMatchKey{{kind: 't', value: v.UTC().Format(time.RFC3339Nano)}}
	case []byte:
		return []scalarMatchKey{{kind: 'x', value: string(v)}}
	case string:
		if expected {
			if strings.HasPrefix(v, `\<ds:`) {
				return []scalarMatchKey{{kind: 's', value: strings.TrimPrefix(v, `\`)}}
			}
			if strings.Contains(v, "<ds:") {
				return nil
			}
		}
		keys := []scalarMatchKey{{kind: 's', value: v}}
		if stamp, err := parseSQLTime(v); err == nil {
			keys = append(keys, scalarMatchKey{kind: 't', value: stamp.UTC().Format(time.RFC3339Nano)})
		}
		if expected {
			if binary, err := base64.StdEncoding.DecodeString(v); err == nil {
				keys = append(keys, scalarMatchKey{kind: 'x', value: string(binary)})
			}
		}
		return keys
	}
	return nil
}

func matchUnorderedRows(spec Expectation, expected, actual structology.Maps, report *Report, ctx *assertly.Context) {
	index := newUnorderedCandidates(actual)
	candidates := make([]*rowCandidates, len(expected))
	for i, row := range expected {
		candidates[i] = index.forRow(row)
	}
	owners := make([]int, len(actual))
	for j := range owners {
		owners[j] = -1
	}
	// Cache tested pairs so augmenting a path never evaluates a predicate twice.
	compared := make([]map[int]bool, len(expected))
	compatible := func(i, j int) bool {
		if matched, found := compared[i][j]; found {
			return matched
		}
		matched := len(compareValue(fmt.Sprintf("rows[%d]", i), expected[i], actual[j], spec.Match.StrictFields, ctx)) == 0
		if compared[i] == nil {
			compared[i] = map[int]bool{}
		}
		compared[i][j] = matched
		return matched
	}
	seen := make([]int, len(actual))
	var augment func(int, int) bool
	augment = func(i, visit int) bool {
		bucket := candidates[i]
		for bucket.free < len(bucket.rows) && owners[bucket.rows[bucket.free]] >= 0 {
			bucket.free++
		}
		// Prefer a free compatible row. Shared cursors make identical duplicate
		// rows cheap and avoid constructing a dense graph of equivalent pairs.
		for _, j := range bucket.rows[bucket.free:] {
			if owners[j] < 0 && seen[j] != visit && compatible(i, j) {
				seen[j] = visit
				owners[j] = i
				return true
			}
		}
		for _, j := range bucket.rows {
			if seen[j] == visit || !compatible(i, j) {
				continue
			}
			seen[j] = visit
			if owners[j] < 0 || augment(owners[j], visit) {
				owners[j] = i
				return true
			}
		}
		return false
	}
	for i, row := range expected {
		if augment(i, i+1) {
			report.Passed++
		} else {
			addMismatch(report, spec, fmt.Sprintf("rows[%d]", i), "missingRow", row, nil)
		}
	}
}
