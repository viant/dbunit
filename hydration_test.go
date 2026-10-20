package dbunit

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/viant/dbunit/fixture"
	"github.com/viant/toolbox/data"
)

func hydratedRows(t *testing.T, result *HydrateResult, index int) []map[string]any {
	t.Helper()
	rows, err := fixture.Inline().Load(context.Background(), result.Tables[index].Source)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestHydrateOrderedGeneratorsCountersAndCaptures(t *testing.T) {
	_, e := testDB(t)
	input := []map[string]any{{"id": "$AsInt(7)", "shared": "$batch", "counter": "$counter"}, {"shared": "$batch", "counter": "$counter", "value": "$AsInt(42)"}}
	req := HydrateRequest{State: data.Map{"counter": int64(9)}, Sequences: []FixtureSequence{{Name: "ids", Start: 100, Step: 2}}, UUID: func() (string, error) { return "f98d67c9-78a4-4b87-bb86-dfe933d472d7", nil }, Tables: []HydrationTable{{Name: "users", Source: maps(input...), Before: []Generation{{State: "batch", UUID: true}}, Generate: []Generation{{Field: []string{"id"}, Sequence: "ids"}}, PostIncrement: []string{"counter"}, Captures: []HydrationCapture{{Path: "records.users", Append: true}, {Field: []string{"id"}, Path: "ids", Append: true}}}}, StateCaptures: []HydrationStateCapture{{From: "counter", Path: "next.counter"}, {From: "batch", Path: "batch"}}}
	result, err := e.Hydrate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	rows := hydratedRows(t, result, 0)
	if number, _ := fixtureInteger(rows[0]["id"]); number != 7 {
		t.Fatal(rows)
	}
	if rows[1]["id"] != int64(100) || rows[0]["counter"] != int64(9) || rows[1]["counter"] != int64(9) {
		t.Fatal(rows)
	}
	if rows[0]["shared"] != rows[1]["shared"] || rows[0]["shared"] != "f98d67c9-78a4-4b87-bb86-dfe933d472d7" {
		t.Fatal(rows)
	}
	if got := result.Sequences["ids"]; got.Next != 102 || got.Step != 2 || got.Allocated != 1 {
		t.Fatal(got)
	}
	if result.Captures["next"].(map[string]any)["counter"] != int64(10) {
		t.Fatal(result.Captures)
	}
	if req.State["counter"] != int64(9) || input[0]["id"] != "$AsInt(7)" {
		t.Fatal("caller mutated", req.State, input)
	}
	rows[0]["shared"] = "mutated"
	if result.Captures["records"].(map[string]any)["users"].([]any)[0].(map[string]any)["shared"] == "mutated" {
		t.Fatal("capture aliases rows")
	}
}
func TestHydrateForwardTaggedReferencesAndExactPaths(t *testing.T) {
	_, e := testDB(t)
	result, err := e.Hydrate(context.Background(), HydrateRequest{Sequences: []FixtureSequence{{Name: "ids", Start: 0}}, Tables: []HydrationTable{
		{Name: "children", Source: maps(map[string]any{"!tag": "child", "parentTag": "$AsString(parent)", "literal.dot": "value"}), TagField: []string{"!tag"}, References: []RecordReference{{Field: []string{"parent_id"}, Table: "parents", Tag: "${row.parentTag}", From: []string{"id"}}, {Field: []string{"parent"}, Table: "parents", Tag: "parent"}}, Captures: []HydrationCapture{{Path: "child"}}},
		{Name: "parents", Source: maps(map[string]any{"!tag": "parent", "name": "${tag}"}), TagField: []string{"!tag"}, Generate: []Generation{{Field: []string{"id"}, Sequence: "ids"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	child := hydratedRows(t, result, 0)[0]
	if child["parent_id"] != int64(0) || child["parent"].(map[string]any)["name"] != "parent" || child["literal.dot"] != "value" {
		t.Fatal(child)
	}
	if _, exists := child["!tag"]; exists {
		t.Fatal("tag directive leaked")
	}
	result.Tags["parents"]["parent"]["id"] = int64(99)
	if hydratedRows(t, result, 1)[0]["id"] != int64(0) || child["parent_id"] != int64(0) {
		t.Fatal("tag result aliases fixture")
	}
}
func TestHydrateUDFOnceAndResolvedPipeline(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,label TEXT,extra TEXT)")
	calls := 0
	state := data.Map{"Tick": func(any, data.Map) (any, error) { calls++; return "$literal", nil }, "literal": "must-not-expand"}
	result, err := e.Hydrate(context.Background(), HydrateRequest{State: state, Tables: []HydrationTable{{Name: "records", Table: Table("records"), Source: maps(map[string]any{"id": 1, "extra": "$Tick()"}), Generate: []Generation{{Field: []string{"label"}, Value: "$Tick()"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("UDF calls", calls)
	}
	rows := hydratedRows(t, result, 0)
	if rows[0]["label"] != "$literal" || rows[0]["extra"] != "$literal" || !result.Tables[0].Source.Expanded {
		t.Fatal(rows, result.Tables[0].Source)
	}
	if _, err := e.Prepare(context.Background(), PrepareRequest{State: state, Datasets: []Dataset{{Table: Table("records"), Source: result.Tables[0].Source}}}); err != nil {
		t.Fatal(err)
	}
	query, err := e.Query(context.Background(), QueryRequest{SQL: "SELECT label,extra FROM records"})
	if err != nil {
		t.Fatal(err)
	}
	if query.Rows[0]["label"] != "$literal" || calls != 2 {
		t.Fatal(query.Rows, calls)
	}
}
func TestHydrateAlreadyExpandedInput(t *testing.T) {
	_, e := testDB(t)
	calls := 0
	state := data.Map{"Tick": func(any, data.Map) (any, error) { calls++; return "expanded", nil }}
	result, err := e.Hydrate(context.Background(), HydrateRequest{State: state, Tables: []HydrationTable{{Name: "rows", Source: fixture.Resolved([]map[string]any{{"value": "$Tick()"}})}}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || hydratedRows(t, result, 0)[0]["value"] != "$Tick()" {
		t.Fatal(calls, result)
	}
}
func TestHydrateUDFFailureIsObservable(t *testing.T) {
	_, e := testDB(t)
	cause := errors.New("custom failure")
	result, err := e.Hydrate(context.Background(), HydrateRequest{State: data.Map{"Fail": func(any, data.Map) (any, error) { return nil, cause }}, Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"value": "$Fail()"})}}})
	if result != nil || !errors.Is(err, cause) {
		t.Fatal(result, err)
	}
}
func TestFixtureSequenceSeedContinuationAndCallerTransaction(t *testing.T) {
	db, e := testDB(t, "CREATE TABLE records(id INTEGER)", "INSERT INTO records VALUES(9)")
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO records VALUES(11)"); err != nil {
		t.Fatal(err)
	}
	seed, err := e.Sequence(ctx, SequenceRequest{Sequences: []FixtureSequence{{Name: "ids", Seed: &QueryRequest{SQL: "SELECT MAX(id)+1 FROM records"}}}}, WithTransaction(tx))
	if err != nil {
		t.Fatal(err)
	}
	if seed.Sequences["ids"].Next != 12 || !seed.Sequences["ids"].Seeded {
		t.Fatal(seed)
	}
	result, err := e.Hydrate(ctx, HydrateRequest{State: data.Map{"prior": seed}, Sequences: []FixtureSequence{{Name: "ids", StartFrom: "prior.sequences.ids.next", Step: -2}}, Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{}, map[string]any{}), Generate: []Generation{{Field: []string{"id"}, Sequence: "ids"}}}}}, WithTransaction(tx))
	if err != nil {
		t.Fatal(err)
	}
	rows := hydratedRows(t, result, 0)
	if rows[0]["id"] != int64(12) || rows[1]["id"] != int64(10) || result.Sequences["ids"].Next != 8 {
		t.Fatal(rows, result)
	}
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil || count != 2 {
		t.Fatal("borrowed transaction ended", count, err)
	}
}
func TestFixtureSequenceSeedCardinalityAndTypes(t *testing.T) {
	_, e := testDB(t)
	for _, query := range []string{"SELECT 1 WHERE 0", "SELECT 1 UNION ALL SELECT 2", "SELECT NULL", "SELECT 1.5", "SELECT 1,2", "SELECT '12'"} {
		t.Run(query, func(t *testing.T) {
			result, err := e.Sequence(context.Background(), SequenceRequest{Sequences: []FixtureSequence{{Name: "ids", Seed: &QueryRequest{SQL: query}}}})
			if err == nil || result != nil {
				t.Fatal(result, err)
			}
		})
	}
}
func TestFixtureSequenceErrorsAndReset(t *testing.T) {
	_, e := testDB(t)
	ctx := context.Background()
	for _, specs := range [][]FixtureSequence{{{Name: "ids"}, {Name: "ids"}}, {{Name: ""}}, {{Name: "ids", StartFrom: "missing"}}, {{Name: "ids", StartFrom: "state++"}}, {{Name: "ids", StartFrom: "state", Seed: &QueryRequest{SQL: "SELECT 1"}}}} {
		if _, err := e.Sequence(ctx, SequenceRequest{Sequences: specs}); err == nil {
			t.Fatal("accepted", specs)
		}
	}
	req := HydrateRequest{Sequences: []FixtureSequence{{Name: "ids", Start: 0}}, Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{}), Generate: []Generation{{Field: []string{"id"}, Sequence: "ids"}}}}}
	first, err := e.Hydrate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Hydrate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.Sequences["ids"].Next != 1 {
		t.Fatal(first, second)
	}
	req.Sequences[0].Start = math.MaxInt64
	if result, err := e.Hydrate(ctx, req); err == nil || result != nil {
		t.Fatal("overflow", result, err)
	}
}
func TestHydrateValidationIsBeforeUDFs(t *testing.T) {
	_, e := testDB(t)
	calls := 0
	state := data.Map{"Tick": func(any, data.Map) (any, error) { calls++; return 1, nil }}
	tests := []HydrateRequest{
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"value": "$Tick()"}), Captures: []HydrationCapture{{Path: "bad++"}}}}},
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"value": "$Tick()"}), Generate: []Generation{{Field: []string{"a"}, Value: 1}, {Field: []string{"a", "b"}, Value: 2}}}}},
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"value": "$Tick()"}), Generate: []Generation{{Field: []string{"id"}, Sequence: "missing"}}}}},
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"value": "$Tick()"}), References: []RecordReference{{Field: []string{"id"}, Table: "missing", Tag: "row"}}}}},
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{}, map[string]any{}), Captures: []HydrationCapture{{Path: "one"}}}}},
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"value": "$Tick()"}), Captures: []HydrationCapture{{Path: "a"}, {Path: "a.b"}}}}},
	}
	for _, req := range tests {
		req.State = state
		if result, err := e.Hydrate(context.Background(), req); err == nil || result != nil {
			t.Fatal("accepted invalid request", req)
		}
	}
	if calls != 0 {
		t.Fatal("UDF ran before static validation", calls)
	}
}
func TestHydrateReferenceFailures(t *testing.T) {
	_, e := testDB(t)
	tests := []HydrateRequest{
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"tag": "same"}, map[string]any{"tag": "same"}), TagField: []string{"tag"}}}},
		{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"tag": "one"}), TagField: []string{"tag"}, References: []RecordReference{{Field: []string{"id"}, Table: "rows", Tag: "missing"}}}}},
		{Tables: []HydrationTable{{Name: "a", Source: maps(map[string]any{"tag": "one"}), TagField: []string{"tag"}, References: []RecordReference{{Field: []string{"b"}, Table: "b", Tag: "two"}}}, {Name: "b", Source: maps(map[string]any{"tag": "two"}), TagField: []string{"tag"}, References: []RecordReference{{Field: []string{"a"}, Table: "a", Tag: "one"}}}}},
	}
	for _, req := range tests {
		if result, err := e.Hydrate(context.Background(), req); err == nil || result != nil {
			t.Fatal(result, err)
		}
	}
}
func TestHydrateUUIDErrorAndCancellation(t *testing.T) {
	_, e := testDB(t)
	cause := errors.New("entropy failure")
	req := HydrateRequest{UUID: func() (string, error) { return "", cause }, Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{}), Generate: []Generation{{Field: []string{"id"}, UUID: true}}}}}
	if result, err := e.Hydrate(context.Background(), req); result != nil || !errors.Is(err, cause) {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := e.Hydrate(ctx, req); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(result, err)
	}
}
func TestHydrateParallelInvocationsAreIsolated(t *testing.T) {
	_, e := testDB(t)
	req := HydrateRequest{State: data.Map{"counter": int64(10)}, Sequences: []FixtureSequence{{Name: "ids", Start: 1}}, Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"counter": "$counter"}), Generate: []Generation{{Field: []string{"id"}, Sequence: "ids"}}, PostIncrement: []string{"counter"}}}}
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := e.Hydrate(context.Background(), req)
			if err != nil {
				t.Error(err)
				return
			}
			if result.Sequences["ids"].Next != 2 {
				t.Error(result)
			}
		}()
	}
	group.Wait()
	if req.State["counter"] != int64(10) {
		t.Fatal(req.State)
	}
}
func TestHydrateNilZeroAndExactValues(t *testing.T) {
	_, e := testDB(t)
	result, err := e.Hydrate(context.Background(), HydrateRequest{Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"present": nil, "zero": json.Number("0")}), Generate: []Generation{{Field: []string{"present"}, Value: "must preserve null"}, {Field: []string{"generatedNull"}, HasValue: true, Value: nil}}, Captures: []HydrationCapture{{Field: []string{"generatedNull"}, Path: "nil"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	row := hydratedRows(t, result, 0)[0]
	if row["present"] != nil || row["generatedNull"] != nil || row["zero"] != json.Number("0") {
		t.Fatal(row)
	}
	if value, present := result.Captures["nil"]; !present || value != nil {
		t.Fatal(result.Captures)
	}
}

func TestHydrateCrossDatabaseParentChildSequences(t *testing.T) {
	_, parentsEngine := testDB(t, "CREATE TABLE parents(id INTEGER)", "INSERT INTO parents VALUES(40)")
	childrenDB, _ := testDB(t, "CREATE TABLE children(id INTEGER)", "INSERT INTO children VALUES(700)")
	if err := WithDatabase("children", childrenDB)(parentsEngine); err != nil {
		t.Fatal(err)
	}
	for _, forward := range []bool{false, true} {
		t.Run(map[bool]string{false: "parents-before-children", true: "children-before-parents"}[forward], func(t *testing.T) {
			parentSource := maps(map[string]any{"tag": "first", "name": "parent-one"}, map[string]any{"tag": "second", "name": "parent-two"})
			childSource := maps(map[string]any{"parentTag": "first", "name": "child-a"}, map[string]any{"parentTag": "second", "name": "child-b"}, map[string]any{"parentTag": "first", "name": "child-c"})
			parent := HydrationTable{Name: "parents", Source: parentSource, TagField: []string{"tag"}, Generate: []Generation{{Field: []string{"id"}, Sequence: "parentIDs", State: "lastParent"}}}
			child := HydrationTable{Name: "children", Source: childSource, Generate: []Generation{{Field: []string{"id"}, Sequence: "childIDs"}}, References: []RecordReference{{Field: []string{"parent_id"}, Table: "parents", Tag: "${row.parentTag}", From: []string{"id"}}}, Captures: []HydrationCapture{{Field: []string{"parent_id"}, Path: "childParents", Append: true}}}
			tables := []HydrationTable{parent, child}
			if forward {
				tables = []HydrationTable{child, parent}
			}
			result, err := parentsEngine.Hydrate(context.Background(), HydrateRequest{Sequences: []FixtureSequence{{Name: "parentIDs", Seed: &QueryRequest{SQL: "SELECT MAX(id)+1 FROM parents"}}, {Name: "childIDs", Seed: &QueryRequest{Database: "children", SQL: "SELECT MAX(id)+1 FROM children"}}}, Tables: tables})
			if err != nil {
				t.Fatal(err)
			}
			childIndex := 1
			if forward {
				childIndex = 0
			}
			rows := hydratedRows(t, result, childIndex)
			for i, want := range []int64{41, 42, 41} {
				if rows[i]["id"] != int64(701+i) || rows[i]["parent_id"] != want {
					t.Fatalf("child %d: %#v expected parent %d", i, rows[i], want)
				}
			}
			if result.Sequences["parentIDs"].Next != 43 || result.Sequences["childIDs"].Next != 704 {
				t.Fatal(result.Sequences)
			}
			if !reflect.DeepEqual(result.Captures["childParents"], []any{int64(41), int64(42), int64(41)}) {
				t.Fatal(result.Captures)
			}
			// Seeding reads both databases and never reserves or modifies their IDs.
			for _, query := range []QueryRequest{{SQL: "SELECT MAX(id) AS value FROM parents"}, {Database: "children", SQL: "SELECT MAX(id) AS value FROM children"}} {
				read, err := parentsEngine.Query(context.Background(), query)
				if err != nil {
					t.Fatal(err)
				}
				if got := read.Rows[0]["value"]; got != int64(40) && got != int64(700) {
					t.Fatal(read.Rows)
				}
			}
		})
	}
}
func TestHydrateReferencesToIndependentScalarFieldsDoNotCycle(t *testing.T) {
	_, e := testDB(t)
	result, err := e.Hydrate(context.Background(), HydrateRequest{Tables: []HydrationTable{
		{Name: "a", Source: maps(map[string]any{"tag": "a", "id": 1}), TagField: []string{"tag"}, References: []RecordReference{{Field: []string{"other_id"}, Table: "b", Tag: "b", From: []string{"id"}}, {Field: []string{"self_id"}, Table: "a", Tag: "a", From: []string{"id"}}}},
		{Name: "b", Source: maps(map[string]any{"tag": "b", "id": 2}), TagField: []string{"tag"}, References: []RecordReference{{Field: []string{"other_id"}, Table: "a", Tag: "a", From: []string{"id"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if a := hydratedRows(t, result, 0)[0]; a["other_id"] != 2 || a["self_id"] != 1 {
		t.Fatal(a)
	}
	if b := hydratedRows(t, result, 1)[0]; b["other_id"] != 1 {
		t.Fatal(b)
	}
}
func TestHydrateRepeatedPhysicalEntriesBeforeAndPostIncrement(t *testing.T) {
	_, e := testDB(t)
	request := HydrateRequest{State: data.Map{"counter": int64(100)}, Tables: []HydrationTable{
		{Name: "first", Table: Table("records"), Source: maps(map[string]any{"id": "$counter", "batch": "$batch"}, map[string]any{"id": "$counter", "batch": "$batch"}), Before: []Generation{{State: "batch", Value: "first"}}, PostIncrement: []string{"counter"}},
		{Name: "empty", Table: Table("records"), Source: maps(), Before: []Generation{{State: "batch", Value: "empty"}}, PostIncrement: []string{"counter"}},
		{Name: "second", Table: Table("records"), Source: maps(map[string]any{"id": "$counter", "batch": "$batch"}), Before: []Generation{{State: "batch", Value: "second"}}, PostIncrement: []string{"counter"}},
	}, StateCaptures: []HydrationStateCapture{{From: "counter", Path: "next"}}}
	result, err := e.Hydrate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first := hydratedRows(t, result, 0)
	second := hydratedRows(t, result, 2)
	if first[0]["id"] != int64(100) || first[1]["id"] != int64(100) || second[0]["id"] != int64(101) || result.Captures["next"] != int64(102) {
		t.Fatal(first, second, result.Captures)
	}
}

func TestHydrateReservedPostIncrementCannotChangeParentRows(t *testing.T) {
	_, e := testDB(t)
	for _, path := range []string{"row.id", "table", "index", "tag", "_udf.counter"} {
		_, err := e.Hydrate(context.Background(), HydrateRequest{Tables: []HydrationTable{{Name: "parents", Source: maps(map[string]any{"id": 1}), PostIncrement: []string{path}}}})
		if err == nil {
			t.Fatal("accepted reserved counter", path)
		}
	}
	result, err := e.Hydrate(context.Background(), HydrateRequest{Tables: []HydrationTable{{Name: "parents", Source: maps(map[string]any{"id": 1})}}, StateCaptures: []HydrationStateCapture{{From: "row.id", Path: "lastParent"}}})
	if err != nil || result.Captures["lastParent"] != 1 {
		t.Fatal(result, err)
	}
}
func TestHydrateNestedNamedMapsAndTypedSlices(t *testing.T) {
	_, e := testDB(t)
	input := map[string]any{"nested": data.Map{"value": "$AsInt(12)"}, "list": []string{"$AsInt(1)", "$AsInt(2)"}}
	result, err := e.Hydrate(context.Background(), HydrateRequest{Tables: []HydrationTable{{Name: "rows", Source: maps(input)}}})
	if err != nil {
		t.Fatal(err)
	}
	row := hydratedRows(t, result, 0)[0]
	if value, _ := fixtureInteger(row["nested"].(map[string]any)["value"]); value != 12 {
		t.Fatal(row)
	}
	for i, value := range row["list"].([]any) {
		if number, _ := fixtureInteger(value); number != int64(i+1) {
			t.Fatal(row)
		}
	}
	if input["nested"].(data.Map)["value"] != "$AsInt(12)" {
		t.Fatal("caller nested map mutated")
	}
}

func TestHydrateExistingTemplateBindsConcreteAliasOnce(t *testing.T) {
	_, e := testDB(t)
	calls := 0
	result, err := e.Hydrate(context.Background(), HydrateRequest{State: data.Map{"Tick": func(any, data.Map) (any, error) { calls++; return int64(27), nil }}, Sequences: []FixtureSequence{{Name: "ids", Start: 100}}, Tables: []HydrationTable{{Name: "rows", Source: maps(map[string]any{"id": "$Tick()"}), Generate: []Generation{{Field: []string{"id"}, State: "currentID", Sequence: "ids"}, {Field: []string{"copy"}, Value: "$currentID"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	row := hydratedRows(t, result, 0)[0]
	if calls != 1 || row["id"] != int64(27) || row["copy"] != int64(27) || result.Sequences["ids"].Allocated != 0 {
		t.Fatal(calls, row, result.Sequences)
	}
}
