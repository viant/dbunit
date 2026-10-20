package dbunit

import (
	"context"
	"database/sql"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/toolbox/data"
	"github.com/viant/toolbox/data/udf"
	_ "modernc.org/sqlite"
	"reflect"
	"testing"
)

func TestPreparationToolboxUDF(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE udf_fixture (id INTEGER PRIMARY KEY, active BOOLEAN, amount REAL, label TEXT)`); err != nil {
		t.Fatal(err)
	}
	engine, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	source := []map[string]any{{"id": "$AsInt($id)", "active": "$AsBool(true)", "amount": "$AsFloat(1.25)", "label": "user-$ToUpper($name)"}}
	_, err = engine.Prepare(context.Background(), PrepareRequest{State: data.Map{"id": "42", "name": "alice"}, Datasets: []Dataset{{Table: Table("udf_fixture"), Source: fixture.Maps(source)}}})
	if err != nil {
		t.Fatal(err)
	}
	var id int
	var active bool
	var amount float64
	var label string
	if err = db.QueryRow(`SELECT id,active,amount,label FROM udf_fixture`).Scan(&id, &active, &amount, &label); err != nil {
		t.Fatal(err)
	}
	if id != 42 || !active || amount != 1.25 || label != "user-ALICE" {
		t.Fatalf("unexpected expanded row: %v %v %v %q", id, active, amount, label)
	}
	if source[0]["id"] != "$AsInt($id)" {
		t.Fatal("caller fixture mutated")
	}
}

func TestToolboxNestedAndCustomUDF(t *testing.T) {
	custom := func(v any, state data.Map) (any, error) { return "custom:" + v.(string), nil }
	state := data.Map{"id": "7", data.UDFKey: data.Map{"ToUpper": custom}}
	registerUDFs(state)
	value, err := expandValue(state, map[string]any{"nested": []any{map[string]any{"id": "$AsInt($id)", "label": "$ToUpper(name)"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"nested": []any{map[string]any{"id": 7, "label": "custom:name"}}}
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("got %#v, want %#v", value, want)
	}
	original, err := udf.Predefined["ToUpper"].(func(any, data.Map) (any, error))("name", nil)
	if err != nil || original != "NAME" {
		t.Fatal("global registry changed")
	}
	other := data.Map{}
	registerUDFs(other)
	got, err := expandValue(other, "$ToUpper(name)", false)
	if err != nil || got != "NAME" {
		t.Fatalf("registry leaked: %v %v", got, err)
	}
}

func TestQueryUDFArgumentsExpandOnce(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	engine, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	state := data.Map{"id": "42", data.UDFKey: data.Map{"Next": func(v any, state data.Map) (any, error) { calls++; return calls, nil }}}
	result, err := engine.Query(context.Background(), QueryRequest{SQL: "SELECT ? AS id, ? AS call, '$AsInt(9)' AS literal", Args: []any{"$AsInt($id)", "$Next(0)"}, State: state})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Rows[0]["id"] != int64(42) || result.Rows[0]["call"] != int64(1) || result.Rows[0]["literal"] != "$AsInt(9)" {
		t.Fatalf("calls=%d rows=%#v", calls, result.Rows)
	}
}
