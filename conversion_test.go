package dbunit

import (
	"context"
	"encoding/json"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/structology"
	"reflect"
	"testing"
	"time"
)

func TestCSVSQLMetadataConversion(t *testing.T) {
	_, e := testDB(t, `CREATE TABLE records(id INTEGER PRIMARY KEY,active BOOLEAN,amount REAL,label TEXT,empty TEXT,nullable INTEGER)`)
	source := fixture.Bytes([]byte("id,active,amount,label,empty,nullable\n1,false,12.25,001,,\\N\n"), "csv")
	_, err := e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: source}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps(map[string]any{"id": 1, "active": false, "amount": 12.25, "label": "001", "empty": "", "nullable": nil})}}})
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestStrictSQLConversion(t *testing.T) {
	fields := []structology.MapField{{Key: "date", Type: reflect.TypeOf((*time.Time)(nil))}, {Key: "decimal", Type: reflect.TypeOf((*string)(nil)), Tag: `dbunit:"decimal"`}, {Key: "id", Type: reflect.TypeOf((*int64)(nil))}}
	row := map[string]any{"date": "2026-10-20T12:13:14Z", "decimal": json.Number("9007199254740993.125"), "id": "9223372036854775807"}
	if err := coerceProjectedRows(structology.Maps{row}, fields); err != nil {
		t.Fatal(err)
	}
	if row["decimal"] != "9007199254740993.125" || row["id"] != int64(9223372036854775807) {
		t.Fatal(row)
	}
	if _, ok := row["date"].(time.Time); !ok {
		t.Fatal(row)
	}
	for _, value := range []string{"9223372036854775808", "1.5", ""} {
		if err := coerceProjectedRows(structology.Maps{{"id": value}}, fields); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func TestTimestampAndBinaryFixtureRoundTrip(t *testing.T) {
	_, e := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,created TIMESTAMP,calendar DATE,time_only TIME,payload BLOB)")
	row := map[string]any{"id": 1, "created": "2026-10-20T12:13:14Z", "calendar": "2026-10-20", "time_only": "12:13:14.123456", "payload": "AAECYWJj/w=="}
	_, err := e.Prepare(context.Background(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: maps(row)}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Expect(context.Background(), ExpectRequest{Expectations: []Expectation{{Table: Table("records"), Expected: maps(row)}}})
	if err != nil {
		t.Fatalf("%+v %v", r, err)
	}
	// Equal instants must compare independently of location and monotonic clocks.
	instant := time.Date(2026, 10, 20, 12, 13, 14, 0, time.UTC)
	if failures := compareValue("time", instant, instant.In(time.FixedZone("offset", 3600)), false, newAssertionContext()); len(failures) > 0 {
		t.Fatal(failures)
	}
}
