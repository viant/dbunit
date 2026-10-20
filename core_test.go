package dbunit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/viant/dbunit/fixture"
	_ "github.com/viant/sqlx/metadata/product/sqlite"
	"github.com/viant/structology"
	"github.com/viant/toolbox/data"
	_ "modernc.org/sqlite"
	"strings"
	"testing"
	"time"
)

func testDB(t *testing.T, statements ...string) (*sql.DB, *Engine) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, s := range statements {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	e, err := New(db, WithDialect("sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return db, e
}
func maps(rows ...map[string]any) fixture.Source { return fixture.Maps(rows) }
func TestPreparationFailureRollsBackAndStops(t *testing.T) {
	db, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,name TEXT)", "INSERT INTO records VALUES(1,'prior')")
	result, err := e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": 2, "name": "staged"})}, {Table: Table("records"), Source: maps(map[string]any{"id": 2, "name": "duplicate"})}, {Table: Table("records"), Source: maps(map[string]any{"id": 3, "name": "later"})}}})
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	if result.Transaction != "rolledBack" {
		t.Fatal(result)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("later dataset executed: %+v", result)
	}
}
func TestPresenceDefaultsNullAndZero(t *testing.T) {
	db, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,name TEXT DEFAULT 'default',qty INTEGER DEFAULT 9)")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := e.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": 1}, map[string]any{"id": 2, "name": nil, "qty": nil}, map[string]any{"id": 0, "name": "", "qty": 0}, map[string]any{"name": "generated"})}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = db
	report, err := e.Expect(ctx, ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps(map[string]any{"id": 0, "name": "", "qty": 0}, map[string]any{"id": 1, "name": "default", "qty": 9}, map[string]any{"id": 2, "name": nil, "qty": nil}, map[string]any{"id": 3, "name": "generated", "qty": 9}), Match: MatchOptions{Keys: []string{"id"}}}}})
	if err != nil {
		t.Fatalf("%v %+v", err, report)
	}
}
func TestExpectationFailureIsMonotonic(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER,name TEXT)", "INSERT INTO records VALUES(1,'actual')")
	report, err := e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{{Name: "fails", Table: Table("records"), Expected: maps(map[string]any{"id": 1, "name": "wrong"})}, {Name: "passes", Table: Table("records"), Expected: maps(map[string]any{"id": 1, "name": "actual"})}}})
	var assertion *AssertionError
	if !errors.As(err, &assertion) || report.Failed != 1 || report.Passed == 0 || report.Err() != err {
		t.Fatalf("%+v %v", report, err)
	}
}
func TestFullKeysAndEmpty(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(a INTEGER,b TEXT)", "INSERT INTO records VALUES(1,'x'),(1,'y'),(2,'z')")
	cases := []struct {
		name      string
		spec      Expectation
		wantError bool
	}{{"full cardinality", Expectation{Table: Table("records"), Expected: maps(map[string]any{"a": 1, "b": "x"})}, true}, {"composite keys", Expectation{Table: Table("records"), Expected: maps(map[string]any{"a": 1, "b": "y"}), Scope: Keys, Match: MatchOptions{Keys: []string{"a", "b"}}}, false}, {"duplicate actual key", Expectation{Table: Table("records"), Expected: maps(map[string]any{"a": 1}), Scope: Keys, Match: MatchOptions{Keys: []string{"a"}}}, true}, {"missing expected tuple", Expectation{Table: Table("records"), Expected: maps(map[string]any{"a": 999}), Scope: Keys, Match: MatchOptions{Keys: []string{"a"}}}, true}, {"empty keys rejected", Expectation{Table: Table("records"), Expected: maps(), Scope: Keys, Match: MatchOptions{Keys: []string{"a"}}}, true}, {"empty keys allowed", Expectation{Table: Table("records"), Expected: maps(), Scope: Keys, Match: MatchOptions{Keys: []string{"a"}, AllowEmpty: true}}, false}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{c.spec}})
			if (err != nil) != c.wantError {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}
func TestMultisetMatchingUsesAugmentingPaths(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER)", "INSERT INTO records VALUES(1),(2)")
	r, err := e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps(map[string]any{"id": "<ds:between[1,2]>"}, map[string]any{"id": 1}), Match: MatchOptions{Multiset: true}}}})
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestNestedJSONAndExactIntegers(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,body TEXT)")
	body := map[string]any{"items": []any{map[string]any{"qty": json.Number("9007199254740993"), "name": "", "nil": nil}, map[string]any{"qty": json.Number("0")}}, "empty": []any{}, "object": map[string]any{}}
	_, err := e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": json.Number("9007199254740993"), "body": body}), Columns: []ColumnSpec{{Name: "body", JSON: true}}}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps(map[string]any{"id": json.Number("9007199254740993"), "body": body}), Match: MatchOptions{JSONColumns: []string{"body"}}}}})
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
	if len(compareValue("value", json.Number("9007199254740993"), int64(9007199254740992), false, newAssertionContext())) == 0 {
		t.Fatal("lost integer precision")
	}
}
func TestCallerTransactionRemainsOwned(t *testing.T) {
	db, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY)")
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": 1})}}}, WithTransaction(tx))
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM records").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestRunActStateRollbackAndCleanup(t *testing.T) {
	db, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,name TEXT)")
	r, err := e.Run(context.Background(), Case{State: data.Map{"id": 1}, Prepare: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": "$id", "name": "before"})}}, Act: func(ctx context.Context, run *Run) error {
		if _, err := run.Executor.ExecContext(ctx, "UPDATE records SET name='after'"); err != nil {
			return err
		}
		return run.Set("name", "after")
	}, Expect: []Expectation{{Table: Table("records"), Expected: maps(map[string]any{"id": "$id", "name": "$name"})}}}, WithTransactionMode(CaseRollback))
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestUpsertSparseAndReplaceEmpty(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,name TEXT DEFAULT 'default',qty INTEGER DEFAULT 9)", "INSERT INTO records VALUES(1,'prior',5)")
	ctx := context.Background()
	_, err := e.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Operation: Upsert, Keys: []string{"id"}, Source: maps(map[string]any{"id": 1, "name": "updated"}, map[string]any{"id": 2, "qty": 0})}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Expect(ctx, ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps(map[string]any{"id": 1, "name": "updated", "qty": 5}, map[string]any{"id": 2, "name": "default", "qty": 0}), Match: MatchOptions{Keys: []string{"id"}}}}})
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
	_, err = e.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Operation: Replace, Source: maps()}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Expect(ctx, ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps()}}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestIdentifierAndSchemaRoundTrip(t *testing.T) {
	_, e := testDB(t)
	ctx := context.Background()
	_, err := e.ApplySchema(ctx, SchemaApplyRequest{Statements: []string{`CREATE TABLE "a.b"("select" INTEGER PRIMARY KEY,"Mixed Case" TEXT DEFAULT 'x;y')`, `CREATE INDEX "my index" ON "a.b"("Mixed Case")`}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table("a.b"), Source: maps(map[string]any{"select": 0, "Mixed Case": "ok"})}}})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := e.ExtractSchema(ctx, SchemaExtractRequest{Tables: []string{"a.b"}})
	if err != nil {
		t.Fatal(err)
	}
	_, target := testDB(t)
	if _, err := target.ApplySchema(ctx, SchemaApplyRequest{Artifact: artifact}); err != nil {
		t.Fatal(err)
	}
	_, err = target.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table("a.b"), Source: maps(map[string]any{"select": 1})}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := target.Expect(ctx, ExpectRequest{Expectations: []Expectation{{Table: Table("a.b"), Expected: maps(map[string]any{"select": 1, "Mixed Case": "x;y"})}}})
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestPreflightDependencyAndNativeCapability(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY)", "INSERT INTO records VALUES(1)")
	_, err := e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Name: "a", Table: Table("records"), Operation: Replace, Source: maps(), DependsOn: []string{"b"}}, {Name: "b", Table: Table("records"), Source: maps(), DependsOn: []string{"a"}}}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatal(err)
	}
	_, err = e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Operation: Replace, Source: maps(map[string]any{"id": 2}), Load: LoadOptions{Strategy: BulkRequired}}}})
	if err == nil {
		t.Fatal("expected native capability error")
	}
}
func TestQueryDuplicateColumnsAndCancellation(t *testing.T) {
	_, e := testDB(t)
	if _, err := e.Query(context.Background(), QueryRequest{SQL: "SELECT 1 AS x,2 AS x"}); err == nil {
		t.Fatal("duplicate aliases")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Query(ctx, QueryRequest{SQL: "SELECT 1"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestMapAdapterNestedPresence(t *testing.T) {
	rows := structology.Maps{{"id": int64(0), "child": map[string]any{"zero": false, "null": nil}, "items": []any{map[string]any{"name": ""}, map[string]any{"name": "a", "n": int64(0)}}}}
	var typed any
	if err := rows.Load(&typed); err != nil {
		t.Fatal(err)
	}
	var restored structology.Maps
	if err := restored.Store(typed); err != nil {
		t.Fatal(err)
	}
	if failures := compareValue("rows", []map[string]any(rows), []map[string]any(restored), true, newAssertionContext()); len(failures) > 0 {
		t.Fatalf("%#v", failures)
	}
}

type alternatingPredicate struct{ calls int }

func (p *alternatingPredicate) Apply(any) bool { p.calls++; return p.calls > 1 }
func TestFailedPredicateIsEvaluatedOnce(t *testing.T) {
	predicate := &alternatingPredicate{}
	report := &Report{}
	compareRows(Expectation{}, structology.Maps{{"id": predicate}}, structology.Maps{{"id": 1}}, report)
	if report.Failed == 0 || predicate.calls != 1 {
		t.Fatalf("%+v calls=%d", report, predicate.calls)
	}
}
func TestNilMacroExpectation(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER,name TEXT)", "INSERT INTO records VALUES(1,NULL)")
	r, err := e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps(map[string]any{"id": 1, "name": "<ds:nil>"})}}})
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRunValidatesExpectationsBeforeWrites(t *testing.T) {
	db, e := testDB(t, "CREATE TABLE records(id INTEGER)")
	_, err := e.Run(context.Background(), Case{Prepare: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": 1})}}, Expect: []Expectation{{Table: Table("records"), Query: &QuerySpec{SQL: "SELECT 1"}, Expected: maps()}}})
	if err == nil {
		t.Fatal("expected shape error")
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestOrderedQueryRequiresOuterOrder(t *testing.T) {
	for _, q := range []string{"SELECT 'ORDER BY' AS x", "SELECT 1 /* ORDER BY x */", "SELECT * FROM (SELECT 1 ORDER BY 1) nested"} {
		if hasOrderBy(q) {
			t.Fatal("accepted unordered result", q)
		}
	}
	for _, q := range []string{"SELECT 1 ORDER BY 1", "SELECT 1 ORDER /* comment */ BY 1", "SELECT 1 ORDER\nBY(1)"} {
		if !hasOrderBy(q) {
			t.Fatal("missed order", q)
		}
	}
}

func TestNamedGeneratedSQLRejectsDifferentDriver(t *testing.T) {
	_, e := testDB(t)
	other, err := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := WithDatabase("other", other)(e); err != nil {
		t.Fatal(err)
	}
	_, err = e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Database: "other", Table: Table("records"), Source: maps()}}})
	if err == nil || !strings.Contains(err.Error(), "different driver") {
		t.Fatal(err)
	}
	_, err = e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{{Database: "other", Table: Table("records"), Expected: maps()}}})
	if err == nil || !strings.Contains(err.Error(), "different driver") {
		t.Fatal(err)
	}
}
