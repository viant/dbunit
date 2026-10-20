package dbunit

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/viant/dbunit/fixture"
	"github.com/viant/toolbox/data"
)

func mappedColumn(name string, path ...string) MappingColumn {
	return MappingColumn{Name: name, Path: path}
}
func compileTestMapping(t *testing.T, spec Mapping) *MappingPlan {
	t.Helper()
	plan, err := CompileMapping(spec)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func mappedRows(t *testing.T, plan *MappingPlan, rows ...map[string]any) []Dataset {
	t.Helper()
	datasets, err := plan.Map(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	return datasets
}

func orderMapping() Mapping {
	return Mapping{Name: "shop", Tables: []MappingTable{
		{Name: "orders", Table: Table("order.table"), Path: []string{"orders"}, Keys: []string{"tenant", "order_id"}, Operation: Replace, Columns: []MappingColumn{{Name: "tenant", Path: []string{"tenant"}, Scope: MappingRoot, Required: true}, {Name: "order_id", Path: []string{"id"}, Required: true}, {Name: "name"}, {Name: "status", Default: "new"}}, Associations: []MappingTable{
			{Name: "lines", Table: Table("line.table"), Path: []string{"lines"}, Keys: []string{"line_id"}, Operation: Replace, DependsOn: []string{"products"}, Columns: []MappingColumn{{Name: "line_id", Path: []string{"id"}, Required: true}, {Name: "tenant", Path: []string{"tenant"}, Scope: MappingRoot}, {Name: "order_id", Path: []string{"order_id"}, Scope: MappingParentRow}, {Name: "product_id", Path: []string{"product", "id"}, Required: true}, {Name: "qty", Default: 0}, {Name: "note", Path: []string{"literal.dot"}, Scope: MappingRoot}}, Associations: []MappingTable{
				{Name: "tags", Table: Table("tag.table"), Path: []string{"tags"}, Keys: []string{"line_id", "tag"}, Operation: Replace, Columns: []MappingColumn{{Name: "line_id", Path: []string{"line_id"}, Scope: MappingParentRow}, {Name: "tag", Scope: MappingElement, Required: true}}},
			}},
		}},
		{Name: "products", Table: Table("product.table"), Path: []string{"orders", "lines", "product"}, Keys: []string{"id"}, Operation: Replace, Columns: []MappingColumn{{Name: "id", Required: true}, {Name: "name"}, {Name: "price"}}},
	}}
}

func TestMappingNestedAssociationsAndPrepare(t *testing.T) {
	plan := compileTestMapping(t, orderMapping())
	rows := []map[string]any{{"tenant": "a.b", "literal.dot": "kept", "orders": []any{
		map[string]any{"id": 0, "name": "first", "lines": []any{
			map[string]any{"id": 0, "product": map[string]any{"id": 9, "name": "shared"}, "tags": []string{"a", "b", "a"}},
			map[string]any{"id": 11, "product": map[string]any{"id": 9, "price": json.Number("4.25")}, "tags": []string{"b"}},
			map[string]any{"id": 12, "product": map[string]any{"id": 10}},
		}},
		map[string]any{"id": 2},
	}}}
	datasets, err := plan.Map(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(datasets))
	for i, dataset := range datasets {
		names[i] = dataset.Name
		if !dataset.Source.Expanded {
			t.Fatal("mapping output is not concrete")
		}
	}
	if !reflect.DeepEqual(names, []string{"orders", "products", "lines", "tags"}) {
		t.Fatal(names)
	}
	if len(datasets[0].Source.Data) != 2 || len(datasets[1].Source.Data) != 2 || len(datasets[2].Source.Data) != 3 || len(datasets[3].Source.Data) != 3 {
		t.Fatalf("wrong cardinality: %+v", datasets)
	}
	product := datasets[1].Source.Data[0]
	if product["name"] != "shared" || product["price"] != json.Number("4.25") {
		t.Fatal(product)
	}
	line := datasets[2].Source.Data[0]
	if line["order_id"] != 0 || line["qty"] != 0 || line["note"] != "kept" {
		t.Fatal(line)
	}
	_, engine := testDB(t, `PRAGMA foreign_keys=ON`,
		`CREATE TABLE "order.table"(tenant TEXT,order_id INTEGER,name TEXT,status TEXT,PRIMARY KEY(tenant,order_id))`,
		`CREATE TABLE "product.table"(id INTEGER PRIMARY KEY,name TEXT DEFAULT 'db-name',price DECIMAL(12,4))`,
		`CREATE TABLE "line.table"(line_id INTEGER PRIMARY KEY,tenant TEXT,order_id INTEGER,product_id INTEGER REFERENCES "product.table"(id),qty INTEGER NOT NULL DEFAULT 9,note TEXT,FOREIGN KEY(tenant,order_id) REFERENCES "order.table"(tenant,order_id))`,
		`CREATE TABLE "tag.table"(line_id INTEGER REFERENCES "line.table"(line_id),tag TEXT,PRIMARY KEY(line_id,tag))`)
	for i := 0; i < 2; i++ {
		if _, err = engine.Prepare(context.Background(), PrepareRequest{Datasets: datasets}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := engine.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{
		{Table: Table("order.table"), Expected: fixture.Maps([]map[string]any{{"tenant": "a.b", "order_id": 0, "name": "first", "status": "new"}, {"tenant": "a.b", "order_id": 2, "name": nil, "status": "new"}}), Match: MatchOptions{Keys: []string{"tenant", "order_id"}}},
		{Table: Table("product.table"), Expected: fixture.Maps([]map[string]any{{"id": 9, "name": "shared", "price": 4.25}, {"id": 10, "name": "db-name", "price": nil}}), Match: MatchOptions{Keys: []string{"id"}}},
		{Table: Table("line.table"), Expected: fixture.Maps([]map[string]any{{"line_id": 0, "tenant": "a.b", "order_id": 0, "product_id": 9, "qty": 0, "note": "kept"}, {"line_id": 11, "tenant": "a.b", "order_id": 0, "product_id": 9, "qty": 0, "note": "kept"}, {"line_id": 12, "tenant": "a.b", "order_id": 0, "product_id": 10, "qty": 0, "note": "kept"}}), Match: MatchOptions{Keys: []string{"line_id"}}},
		{Table: Table("tag.table"), Expected: fixture.Maps([]map[string]any{{"line_id": 0, "tag": "a"}, {"line_id": 0, "tag": "b"}, {"line_id": 11, "tag": "b"}}), Match: MatchOptions{Keys: []string{"line_id", "tag"}}},
	}})
	if err != nil {
		t.Fatal(err, report)
	}
}

func TestMappingTypedCompositeKeysAndConflict(t *testing.T) {
	plan := compileTestMapping(t, Mapping{Name: "typed", Tables: []MappingTable{{Name: "rows", Table: Table("physical"), Keys: []string{"a", "b"}, Columns: []MappingColumn{{Name: "a"}, {Name: "b"}, {Name: "value"}, {Name: "extra"}}}}})
	rows := []map[string]any{{"a": 1, "b": 23, "value": nil}, {"a": 12, "b": 3, "value": "different"}, {"a": "1", "b": 23, "value": "string"}, {"a": json.Number("1.00"), "b": int64(23), "extra": false}}
	datasets := mappedRows(t, plan, rows...)
	if len(datasets[0].Source.Data) != 3 {
		t.Fatal(datasets)
	}
	first := datasets[0].Source.Data[0]
	if value, present := first["value"]; !present || value != nil || first["extra"] != false {
		t.Fatal(first)
	}
	rows = append(rows, map[string]any{"a": 1, "b": 23, "value": "conflicts with null"})
	output, err := plan.Map(context.Background(), rows)
	if output != nil || !errors.Is(err, ErrMappingConflict) {
		t.Fatal(output, err)
	}
	var conflict *MappingConflictError
	if !errors.As(err, &conflict) || conflict.Column != "value" || conflict.Row != 5 || conflict.Existing != nil || conflict.Incoming != "conflicts with null" {
		t.Fatal(err)
	}
	// Omitting Keys preserves every record, even identical ones.
	unkeyed := compileTestMapping(t, Mapping{Name: "append", Tables: []MappingTable{{Name: "all", Table: Table("all"), Columns: []MappingColumn{{Name: "a"}}}}})
	if got := mappedRows(t, unkeyed, map[string]any{"a": 1}, map[string]any{"a": 1}); len(got[0].Source.Data) != 2 {
		t.Fatal(got)
	}
}

func TestMappingPresenceRequiredAndDefault(t *testing.T) {
	var spec Mapping
	err := json.Unmarshal([]byte(`{"name":"presence","tables":[{"name":"rows","table":"rows","keys":["id"],"columns":[{"name":"id","required":true},{"name":"number","default":9007199254740993},{"name":"nullable","default":null},{"name":"optional"},{"name":"value","default":"literal $notExpanded"}]}]}`), &spec)
	if err != nil {
		t.Fatal(err)
	}
	plan := compileTestMapping(t, spec)
	datasets := mappedRows(t, plan, map[string]any{"id": 0, "value": nil}, map[string]any{"id": 1, "number": 0, "nullable": "present", "optional": false})
	first, second := datasets[0].Source.Data[0], datasets[0].Source.Data[1]
	if first["number"] != json.Number("9007199254740993") || first["value"] != nil {
		t.Fatal(first)
	}
	if value, present := first["nullable"]; !present || value != nil {
		t.Fatal(first)
	}
	if _, present := first["optional"]; present {
		t.Fatal(first)
	}
	if second["number"] != 0 || second["optional"] != false || second["nullable"] != "present" || second["value"] != "literal $notExpanded" {
		t.Fatal(second)
	}
	_, err = plan.Map(context.Background(), []map[string]any{{"id": nil}})
	if err == nil {
		t.Fatal("null key accepted")
	}
	// Skip is explicit, and skips descendants rather than creating orphan rows.
	skip := compileTestMapping(t, Mapping{Name: "skip", Tables: []MappingTable{
		{Name: "parent", Table: Table("parent"), Keys: []string{"id"}, OnMissingRequired: MissingRequiredSkip,
			Columns: []MappingColumn{{Name: "id"}},
			Associations: []MappingTable{
				{Name: "child", Table: Table("child"), Columns: []MappingColumn{{Name: "id", Path: []string{"child"}}}},
			},
		},
	}})
	empty := mappedRows(t, skip, map[string]any{"child": 3})
	if len(empty) != 2 || len(empty[0].Source.Data) != 0 || len(empty[1].Source.Data) != 0 {
		t.Fatal(empty)
	}
	// A parent source reference is independent of destination column renaming.
	parent := compileTestMapping(t, Mapping{Name: "parentRefs", Tables: []MappingTable{
		{Name: "p", Table: Table("p"), Columns: []MappingColumn{{Name: "renamed", Path: []string{"original"}}},
			Associations: []MappingTable{
				{Name: "c", Table: Table("c"), Path: []string{"children"}, Columns: []MappingColumn{
					{Name: "from_source", Path: []string{"original"}, Scope: MappingParent},
					{Name: "from_row", Path: []string{"renamed"}, Scope: MappingParentRow},
				}},
			},
		},
	}})
	got := mappedRows(t, parent, map[string]any{"original": "value", "children": []any{map[string]any{}}})
	if !reflect.DeepEqual(got[1].Source.Data[0], map[string]any{"from_source": "value", "from_row": "value"}) {
		t.Fatal(got)
	}
}

func TestMappingSnapshotsAndConcurrentRuns(t *testing.T) {
	defaultObject := map[string]any{"nested": []any{"initial"}}
	spec := Mapping{Name: "immutable", Tables: []MappingTable{{Name: "rows", Table: Table("literal.dot"), Columns: []MappingColumn{{Name: "id"}, {Name: "value", Default: defaultObject}, {Name: "literal", Default: "$literal"}}}}}
	plan := compileTestMapping(t, spec)
	defaultObject["nested"].([]any)[0] = "mutated"
	spec.Tables[0].Table[0] = "mutated"
	spec.Tables[0].Columns[0].Name = "mutated"
	input := []map[string]any{{"id": 1}}
	initial := mappedRows(t, plan, input...)
	initial[0].Source.Data[0]["value"].(map[string]any)["nested"].([]any)[0] = "changed output"
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := plan.Map(context.Background(), input)
			if err == nil && (got[0].Table[0] != "literal.dot" || got[0].Source.Data[0]["value"].(map[string]any)["nested"].([]any)[0] != "initial") {
				err = errors.New("shared mapping data")
			}
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(input, []map[string]any{{"id": 1}}) {
		t.Fatal(input)
	}
	// Mapping sources are concrete: Prepare must preserve dollar-like literals.
	db, engine := testDB(t, `CREATE TABLE "literal.dot"(id INTEGER,value TEXT,literal TEXT)`)
	simple := compileTestMapping(t, Mapping{Name: "literal", Tables: []MappingTable{{Name: "literal", Table: Table("literal.dot"), Columns: []MappingColumn{{Name: "id"}, {Name: "literal", Default: "$literal"}}}}})
	output := mappedRows(t, simple, map[string]any{"id": 1})
	if _, err := engine.Prepare(context.Background(), PrepareRequest{Datasets: output, State: data.Map{"literal": "expanded incorrectly"}}); err != nil {
		t.Fatal(err)
	}
	var actual string
	if err := db.QueryRow(`SELECT literal FROM "literal.dot"`).Scan(&actual); err != nil || actual != "$literal" {
		t.Fatal(actual, err)
	}
}
