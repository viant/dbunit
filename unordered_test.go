package dbunit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/structology"
	"math/rand"
	"testing"
	"time"
)

func TestLargeUnorderedExpect(t *testing.T) {
	type useCase struct {
		description string
		count       int
		duplicate   bool
		missing     bool
		contains    bool
		failed      bool
	}
	for _, tc := range []useCase{
		{description: "257 shuffled rows", count: 257},
		{description: "4096 shuffled rows", count: 4096},
		{description: "4096 duplicate rows", count: 4096, duplicate: true},
		{description: "duplicate multiplicity differs", count: 513, duplicate: true, missing: true, failed: true},
		{description: "contains preserves duplicate multiplicity", count: 513, duplicate: true, contains: true},
	} {
		t.Run(tc.description, func(t *testing.T) {
			_, engine := testDB(t, "CREATE TABLE records(value INTEGER, name TEXT)")
			rows := make([]map[string]any, tc.count)
			for i := range rows {
				value := i
				if tc.duplicate {
					value = 7
				}
				rows[i] = map[string]any{"value": value, "name": "fixture"}
			}
			if _, err := engine.Prepare(t.Context(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: fixture.Resolved(rows)}}}); err != nil {
				t.Fatal(err)
			}
			expected := make([]map[string]any, len(rows))
			for i := range rows {
				expected[len(rows)-i-1] = rows[i]
			}
			if tc.missing {
				expected[0] = map[string]any{"value": 8, "name": "fixture"}
			}
			if tc.contains {
				expected = expected[:len(expected)-1]
			}
			report, err := engine.Expect(t.Context(), ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: fixture.Resolved(expected), Match: MatchOptions{Multiset: true, StrictFields: true, Contains: tc.contains}}}})
			if (err != nil) != tc.failed {
				t.Fatalf("failed=%t report=%+v error=%v", tc.failed, report, err)
			}
			for _, mismatch := range report.Mismatches {
				if mismatch.Category == "matchingLimit" {
					t.Fatal("row count must not prohibit an unordered comparison")
				}
			}
		})
	}
}

func TestLargeUnorderedPredicatesReassignMatches(t *testing.T) {
	expected, actual := make(structology.Maps, 513), make(structology.Maps, 513)
	for i := range actual {
		actual[i] = map[string]any{"group": i / 2, "value": i % 2}
		value := any(0)
		if i%2 == 0 && i+1 < len(actual) {
			value = "<ds:between[0,1]>"
		}
		expected[i] = map[string]any{"group": i / 2, "value": value}
	}
	report := &Report{}
	compareRows(Expectation{Match: MatchOptions{Multiset: true, StrictFields: true}}, expected, actual, report)
	if report.Failed != 0 {
		t.Fatalf("overlapping predicates require reassignment: %+v", report)
	}
	// Each broad predicate has to leave the single zero row for its narrow peer.
	actual[1]["value"] = 0
	expected[0]["value"] = 1
	report = &Report{}
	compareRows(Expectation{Match: MatchOptions{Multiset: true}}, expected, actual, report)
	if report.Failed == 0 {
		t.Fatal("a missing value must not be satisfied by reusing a duplicate")
	}
}

type countingRowPredicate struct{ calls map[int]int }

func (p countingRowPredicate) Apply(value any) bool {
	n, ok := value.(int)
	if !ok {
		return false
	}
	p.calls[n]++
	return n == 1 || n == 2
}

func TestUnorderedPredicatePairsAreEvaluatedOnce(t *testing.T) {
	predicate := countingRowPredicate{calls: map[int]int{}}
	report := &Report{}
	compareRows(Expectation{Match: MatchOptions{Multiset: true}}, structology.Maps{{"value": predicate}, {"value": 1}}, structology.Maps{{"value": 1}, {"value": 2}}, report)
	if report.Failed != 0 || predicate.calls[1] != 1 || predicate.calls[2] != 1 {
		t.Fatalf("report=%+v predicate calls=%v", report, predicate.calls)
	}
}

func TestUnorderedIndexPreservesValueSemantics(t *testing.T) {
	stamp := time.Date(2026, 10, 21, 1, 2, 3, 0, time.UTC)
	type useCase struct {
		description string
		expected    any
		actual      any
	}
	for _, tc := range []useCase{
		{"exact integer across types", json.Number("9007199254740993"), uint64(9007199254740993)},
		{"decimal across numeric types", json.Number("1.25"), float32(1.25)},
		{"time expected string", "2026-10-21 01:02:03", stamp},
		{"time actual string", stamp, "2026-10-21 01:02:03"},
		{"binary base64 string", base64.StdEncoding.EncodeToString([]byte{0, 1, 255}), []byte{0, 1, 255}},
		{"binary numeric slice", []byte{0, 1, 255}, []int{0, 1, 255}},
		{"unindexed nested object", map[string]any{"amount": 1}, map[string]any{"amount": int64(1)}},
		{"unindexed array", [2]int{1, 2}, []int64{1, 2}},
		{"literal escaped macro", `\<ds:nil>`, "<ds:nil>"},
		{"nil", nil, nil},
		{"empty binary", []byte{}, []byte{}},
		{"typed nil binary", []byte(nil), []byte(nil)},
	} {
		t.Run(tc.description, func(t *testing.T) {
			// Two rows ensure this exercises indexing, rather than the singleton
			// diagnostic path; reverse them to require unordered selection.
			expected := structology.Maps{{"value": tc.expected}, {"other": true}}
			actual := structology.Maps{{"other": true}, {"value": tc.actual}}
			report := &Report{}
			compareRows(Expectation{Match: MatchOptions{Multiset: true, StrictFields: true}}, expected, actual, report)
			if report.Failed != 0 {
				t.Fatalf("compatible values were excluded by the index: %+v", report)
			}
		})
	}
}

// An exhaustive small matching oracle checks the optimization independently of
// its indexing and augmenting-path implementation.
func TestUnorderedMatchingAgreesWithExhaustiveSearch(t *testing.T) {
	random := rand.New(rand.NewSource(37))
	values := []any{nil, false, true, 0, int64(1), json.Number("1.25"), float64(1.25), "1", "<ds:between[0,1]>", []byte{0}, []int{0}, map[string]any{"nested": 1}}
	for iteration := 0; iteration < 500; iteration++ {
		makeRows := func() structology.Maps {
			rows := make(structology.Maps, random.Intn(5))
			for i := range rows {
				row := map[string]any{}
				for _, key := range []string{"a", "b"} {
					if random.Intn(3) > 0 {
						row[key] = values[random.Intn(len(values))]
					}
				}
				rows[i] = row
			}
			return rows
		}
		expected, actual := makeRows(), makeRows()
		match := MatchOptions{Multiset: true, StrictFields: random.Intn(2) == 0, Contains: random.Intn(2) == 0}
		used := make([]bool, len(actual))
		var exhaustive func(int) bool
		exhaustive = func(i int) bool {
			if i == len(expected) {
				return true
			}
			for j, row := range actual {
				if !used[j] && len(compareValue("row", expected[i], row, match.StrictFields, newAssertionContext())) == 0 {
					used[j] = true
					if exhaustive(i + 1) {
						return true
					}
					used[j] = false
				}
			}
			return false
		}
		want := (match.Contains || len(expected) == len(actual)) && exhaustive(0)
		report := &Report{}
		compareRows(Expectation{Match: match}, expected, actual, report)
		if (report.Failed == 0) != want {
			t.Fatalf("iteration=%d expected=%#v actual=%#v options=%+v report=%+v", iteration, expected, actual, match, report)
		}
	}
}

func BenchmarkUnorderedMatching(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		for _, duplicates := range []bool{false, true} {
			b.Run(fmt.Sprintf("rows=%d/duplicates=%t", count, duplicates), func(b *testing.B) {
				expected, actual := make(structology.Maps, count), make(structology.Maps, count)
				for i := range expected {
					value := i
					if duplicates {
						value = 1
					}
					expected[i] = map[string]any{"value": value}
					actual[count-i-1] = map[string]any{"value": int64(value)}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					report := &Report{}
					compareRows(Expectation{Match: MatchOptions{Multiset: true, StrictFields: true}}, expected, actual, report)
					if report.Failed != 0 {
						b.Fatal(report)
					}
				}
			})
		}
	}
}

func TestLargeUnorderedPredicatesWithoutLiteralAnchor(t *testing.T) {
	expected, actual := make(structology.Maps, 513), make(structology.Maps, 513)
	expected[0] = map[string]any{"value": "<ds:between[0,512]>"}
	for i := range actual {
		actual[i] = map[string]any{"value": i}
		if i > 0 {
			expected[i] = map[string]any{"value": i - 1}
		}
	}
	report := &Report{}
	compareRows(Expectation{Match: MatchOptions{Multiset: true}}, expected, actual, report)
	if report.Failed != 0 {
		t.Fatalf("unanchored predicate must move to leave each narrow value available: %+v", report)
	}
}
