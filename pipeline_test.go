package dbunit

import (
	"context"
	"encoding/json"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/toolbox/data"
	"testing"
)

func TestHydrateMapPreparePipeline(t *testing.T) {
	db, engine := testDB(t,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE customers(id INTEGER PRIMARY KEY,name TEXT)`,
		`CREATE TABLE orders(id INTEGER PRIMARY KEY,customer_id INTEGER REFERENCES customers(id),label TEXT)`,
		`CREATE TABLE order_items(order_id INTEGER REFERENCES orders(id),sku TEXT,quantity INTEGER,PRIMARY KEY(order_id,sku))`)
	ctx := context.Background()
	calls := 0
	source := fixture.Maps([]map[string]any{
		{"customer": map[string]any{"id": 7, "name": "Alice"}, "label": "$Literal(0)", "items": []any{map[string]any{"sku": "A", "quantity": "$AsInt(2)"}}},
		{"customer": map[string]any{"id": 7}, "label": "second", "items": []any{map[string]any{"sku": "B", "quantity": 1}}},
	})
	hydrated, err := engine.Hydrate(ctx, HydrateRequest{
		State:     data.Map{"Literal": func(any, data.Map) (any, error) { calls++; return "$Literal(0)", nil }},
		Sequences: []FixtureSequence{{Name: "orderID", Start: 100}},
		Tables:    []HydrationTable{{Name: "orders", Source: source, Generate: []Generation{{Field: []string{"id"}, Sequence: "orderID"}}, Captures: []HydrationCapture{{Field: []string{"id"}, Path: "orderIDs", Append: true}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mapping := Mapping{Name: "orders", Tables: []MappingTable{
		{Name: "customers", Table: Table("customers"), Path: []string{"customer"}, Keys: []string{"id"}, Columns: []MappingColumn{{Name: "id"}, {Name: "name"}}},
		{Name: "orders", Table: Table("orders"), Keys: []string{"id"}, DependsOn: []string{"customers"}, Columns: []MappingColumn{{Name: "id"}, {Name: "customer_id", Path: []string{"customer", "id"}}, {Name: "label"}}, Associations: []MappingTable{{Name: "items", Table: Table("order_items"), Path: []string{"items"}, Keys: []string{"order_id", "sku"}, Columns: []MappingColumn{{Name: "order_id", Scope: MappingParentRow, Path: []string{"id"}}, {Name: "sku"}, {Name: "quantity"}}}}},
	}}
	mapped, err := engine.Map(ctx, MapRequest{Source: hydrated.Tables[0].Source, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the same serialized boundary used by declarative workflow captures.
	content, err := json.Marshal(mapped)
	if err != nil {
		t.Fatal(err)
	}
	var restored MapResult
	if err = json.Unmarshal(content, &restored); err != nil {
		t.Fatal(err)
	}
	prepared, err := engine.Prepare(ctx, PrepareRequest{Datasets: restored.Datasets, State: data.Map{"Literal": func(any, data.Map) (any, error) { calls++; return "wrong", nil }}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Rows != 5 || calls != 1 || hydrated.Sequences["orderID"].Next != 102 {
		t.Fatalf("prepared=%+v calls=%d sequence=%+v", prepared, calls, hydrated.Sequences)
	}
	var literal string
	if err = db.QueryRow("SELECT label FROM orders WHERE id=100").Scan(&literal); err != nil || literal != "$Literal(0)" {
		t.Fatalf("%q %v", literal, err)
	}
	report, err := engine.Expect(ctx, ExpectRequest{Expectations: []Expectation{
		{Table: Table("customers"), Expected: fixture.Resolved([]map[string]any{{"id": 7, "name": "Alice"}}), Match: MatchOptions{Keys: []string{"id"}}},
		{Table: Table("order_items"), Expected: fixture.Resolved([]map[string]any{{"order_id": 100, "sku": "A", "quantity": 2}, {"order_id": 101, "sku": "B", "quantity": 1}}), Match: MatchOptions{Keys: []string{"order_id", "sku"}}},
	}})
	if err != nil {
		t.Fatalf("%v %+v", err, report)
	}
	if _, exists := source.Data[0]["id"]; exists {
		t.Fatal("source mutated")
	}
}
