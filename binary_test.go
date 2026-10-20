package dbunit

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/viant/dbunit/fixture"
)

func TestCloneBinaryPreservesNullEmptyAndOwnership(t *testing.T) {
	type useCase struct {
		description string
		input       []byte
		expected    []byte
	}
	for _, tc := range []useCase{
		{description: "null", input: nil, expected: nil},
		{description: "empty blob", input: []byte{}, expected: []byte{}},
		{description: "binary blob", input: []byte{0, 1, 255}, expected: []byte{0, 1, 255}},
	} {
		t.Run(tc.description, func(t *testing.T) {
			got := cloneValue(tc.input).([]byte)
			if !reflect.DeepEqual(tc.expected, got) {
				t.Fatalf("clone=%#v, expected=%#v", got, tc.expected)
			}
			if len(got) > 0 {
				got[0] = 99
				if !reflect.DeepEqual(tc.input, tc.expected) {
					t.Fatal("clone aliases source bytes")
				}
			}
		})
	}
}

func TestPrepareBinaryPreservesSQLNullAndEmpty(t *testing.T) {
	type useCase struct {
		description string
		input       any
		expected    string
	}
	for _, tc := range []useCase{
		{description: "null", input: nil, expected: "null"},
		{description: "nil bytes", input: []byte(nil), expected: "null"},
		{description: "empty blob", input: []byte{}, expected: "blob"},
		{description: "binary blob", input: []byte{0, 1, 255}, expected: "blob"},
	} {
		for _, kind := range []string{"maps", "typed"} {
			t.Run(kind+"/"+tc.description, func(t *testing.T) {
				db, engine := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,payload BLOB)")
				source := maps(map[string]any{"id": 1, "payload": tc.input})
				if kind == "typed" {
					type row struct {
						ID      int    `json:"id"`
						Payload []byte `json:"payload"`
					}
					payload, _ := tc.input.([]byte)
					source = fixture.Typed([]row{{ID: 1, Payload: payload}})
				}
				_, err := engine.Prepare(t.Context(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: source}}})
				if err != nil {
					t.Fatal(err)
				}
				var storage string
				var actual any
				if err := db.QueryRow("SELECT typeof(payload),payload FROM records").Scan(&storage, &actual); err != nil {
					t.Fatal(err)
				}
				if storage != tc.expected {
					t.Fatalf("storage=%s, expected %s", storage, tc.expected)
				}
				if tc.expected == "null" {
					if actual != nil {
						t.Fatalf("SQL NULL scanned as %#v", actual)
					}
				} else {
					// SQLite's modernc driver may scan an empty BLOB as typed nil
					// bytes. The SQL storage type above proves it is not NULL.
					actualBytes, ok := actual.([]byte)
					if !ok || !bytes.Equal(tc.input.([]byte), actualBytes) {
						t.Fatalf("payload=%#v, expected %#v", actual, tc.input)
					}
				}
			})
		}
	}
}

func TestQueryBinaryPreservesSQLNullAndEmpty(t *testing.T) {
	type useCase struct {
		description string
		input       string
		expected    any
	}
	for _, tc := range []useCase{
		{description: "null", input: "NULL", expected: nil},
		{description: "empty blob", input: "X''", expected: []byte{}},
		{description: "binary blob", input: "X'0001ff'", expected: []byte{0, 1, 255}},
	} {
		t.Run(tc.description, func(t *testing.T) {
			_, engine := testDB(t, "CREATE TABLE records(payload BLOB)", "INSERT INTO records VALUES("+tc.input+")")
			result, err := engine.Query(t.Context(), QueryRequest{SQL: "SELECT payload FROM records"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Rows) != 1 || !reflect.DeepEqual(tc.expected, result.Rows[0]["payload"]) {
				t.Fatalf("rows=%#v, expected payload=%#v", result.Rows, tc.expected)
			}
		})
	}
}
