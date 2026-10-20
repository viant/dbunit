package dbunit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	sqlxio "github.com/viant/sqlx/io"
	"github.com/viant/sqlx/loption"
	"github.com/viant/sqlx/metadata/info"
	"github.com/viant/sqlx/metadata/registry"
	"github.com/viant/structology"
	"reflect"
	"testing"
)

type nativeProbe struct {
	calls    int
	failAt   int
	payloads []structology.Maps
	txs      []*sql.Tx
}

func (p *nativeProbe) Exec(ctx context.Context, payload any, db *sql.DB, table string, options ...loption.Option) (sql.Result, error) {
	p.calls++
	opts := loption.NewOptions(options...)
	p.txs = append(p.txs, opts.GetTransaction())
	rows, ok := payload.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected presence-filtered JSON maps, got %T", payload)
	}
	p.payloads = append(p.payloads, rows)
	if p.calls == p.failAt {
		return nil, errors.New("native failed")
	}
	ex := Executor(db)
	if opts.GetTransaction() != nil {
		ex = opts.GetTransaction()
	}
	return ex.ExecContext(ctx, "INSERT INTO "+table+"(id) VALUES (?)", p.calls)
}
func TestNativeAdapterTransactionAndPresenceBridge(t *testing.T) {
	db, _ := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,name TEXT DEFAULT 'default')")
	probe := &nativeProbe{failAt: 2}
	registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return probe }, "SQLite")
	t.Cleanup(func() { registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return nil }, "SQLite") })
	e, err := New(db, WithDialect("sqlite"), WithNativeLoad(NativeLoadOptions{Transactional: true, Format: "json"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Load: LoadOptions{Strategy: BulkRequired}, Source: maps(map[string]any{"id": 0, "name": nil}, map[string]any{"id": 1})}}})
	if err == nil || probe.calls != 2 || result.Transaction != "rolledBack" {
		t.Fatalf("%+v %+v %v", probe, result, err)
	}
	if probe.txs[0] == nil || probe.txs[0] != probe.txs[1] {
		t.Fatal("owned transaction was not forwarded")
	}
	if v, ok := probe.payloads[0][0]["name"]; !ok || v != nil {
		t.Fatal("explicit NULL lost")
	}
	if _, ok := probe.payloads[1][0]["name"]; ok {
		t.Fatal("absent field appeared")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestNativeCapabilityRejectedBeforeReplace(t *testing.T) {
	db, _ := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY)", "INSERT INTO records VALUES(1)")
	probe := &nativeProbe{}
	registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return probe }, "SQLite")
	t.Cleanup(func() { registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return nil }, "SQLite") })
	e, err := New(db, WithDialect("sqlite"), WithNativeLoad(NativeLoadOptions{Format: "json"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Operation: Replace, Load: LoadOptions{Strategy: BulkRequired}, Source: maps(map[string]any{"id": 2})}}})
	if err == nil || probe.calls != 0 {
		t.Fatal(err, probe.calls)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestNativeJSONNestedPresence(t *testing.T) {
	db, _ := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,body TEXT,items TEXT)")
	probe := &nativeProbe{}
	registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return probe }, "SQLite")
	t.Cleanup(func() { registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return nil }, "SQLite") })
	e, err := New(db, WithDialect("sqlite"), WithNativeLoad(NativeLoadOptions{Transactional: true, Format: "JSON"}))
	if err != nil {
		t.Fatal(err)
	}
	nested := []structology.MapField{{Key: "zero", Type: reflect.TypeOf((*int64)(nil))}, {Key: "null", Type: reflect.TypeOf((*string)(nil))}, {Key: "absent", Type: reflect.TypeOf((*string)(nil))}}
	_, err = e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Load: LoadOptions{Strategy: BulkRequired}, Columns: []ColumnSpec{{Name: "body", Fields: nested}, {Name: "items", Fields: nested, Repeated: true}}, Source: maps(map[string]any{"id": 0, "body": map[string]any{"zero": int64(0), "null": nil}, "items": []any{map[string]any{"zero": int64(0)}, map[string]any{"null": nil}}})}}})
	if err != nil {
		t.Fatal(err)
	}
	payload := probe.payloads[0][0]
	body := payload["body"].(map[string]any)
	if _, ok := body["absent"]; ok {
		t.Fatal(body)
	}
	if value, ok := body["null"]; !ok || value != nil {
		t.Fatal(body)
	}
	if body["zero"] != int64(0) {
		t.Fatal(body)
	}
	items := payload["items"].([]map[string]any)
	if len(items) != 2 || len(items[0]) != 1 || len(items[1]) != 1 {
		t.Fatal(items)
	}
	if _, ok := body["Has"]; ok {
		t.Fatal("presence marker leaked")
	}
}

func TestNativeAutoFallsBackBeforeWrites(t *testing.T) {
	db, _ := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY)")
	probe := &nativeProbe{}
	registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return probe }, "SQLite")
	t.Cleanup(func() { registry.RegisterLoad(func(*info.Dialect) sqlxio.LoadExecutor { return nil }, "SQLite") })
	e, err := New(db, WithDialect("sqlite"), WithNativeLoad(NativeLoadOptions{MinRows: 1}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": 1})}}})
	if err != nil || probe.calls != 0 || len(r.Steps) != 1 || r.Steps[0].Strategy != Batch {
		t.Fatalf("%+v calls=%d %v", r, probe.calls, err)
	}
}
