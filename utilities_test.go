package dbunit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/viant/dbunit/fixture"
	"testing"
)

func TestExportAndCompare(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE export_rows(id INTEGER PRIMARY KEY, label TEXT); INSERT INTO export_rows VALUES(9007199254740993,'exact')`)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, format := range []string{"json", "ndjson"} {
		exported, err := engine.Export(ctx, ExportRequest{Query: QueryRequest{SQL: "SELECT * FROM export_rows ORDER BY id"}, Format: format})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := fixture.Inline().Load(ctx, fixture.Bytes(exported.Content, format))
		if err != nil {
			t.Fatal(err)
		}
		if exported.Rows != 1 || rows[0]["id"] != json.Number("9007199254740993") {
			t.Fatalf("precision lost: %#v", rows)
		}
	}
	req := CompareRequest{Expected: QueryRequest{SQL: "SELECT * FROM export_rows"}, Actual: QueryRequest{SQL: "SELECT * FROM export_rows"}, Match: MatchOptions{Keys: []string{"id"}}}
	report, err := engine.Compare(ctx, req)
	if err != nil || report.Failed != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	req.Actual.SQL = "SELECT id,'wrong' AS label FROM export_rows"
	report, err = engine.Compare(ctx, req)
	var mismatch *AssertionError
	if !errors.As(err, &mismatch) || report.Failed == 0 {
		t.Fatalf("missing mismatch %+v %v", report, err)
	}
	if _, err = engine.Export(ctx, ExportRequest{Format: "csv"}); err == nil {
		t.Fatal("unsupported format accepted")
	}
}

func TestCompareRejectsKeysAfterJSONNormalization(t *testing.T) {
	_, engine := testDB(t, `CREATE TABLE unused(id INTEGER)`)
	_, err := engine.Compare(context.Background(), CompareRequest{
		Expected: QueryRequest{SQL: "SELECT '1' AS k UNION ALL SELECT '1.0' AS k"},
		Actual:   QueryRequest{SQL: "SELECT '1' AS k UNION ALL SELECT '2' AS k"},
		Match:    MatchOptions{Keys: []string{"k"}, JSONColumns: []string{"k"}},
	})
	if err == nil {
		t.Fatal("expected duplicate normalized key error")
	}
}

func TestExportEmptyNDJSONRoundTrip(t *testing.T) {
	_, engine := testDB(t, `CREATE TABLE empty_rows(id INTEGER)`)
	exported, err := engine.Export(context.Background(), ExportRequest{Query: QueryRequest{SQL: "SELECT * FROM empty_rows"}, Format: "ndjson"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.Inline().Load(context.Background(), fixture.Bytes(exported.Content, "ndjson"))
	if err != nil || len(rows) != 0 {
		t.Fatalf("%v %v", rows, err)
	}
}
