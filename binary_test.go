package dbunit

import (
	"reflect"
	"testing"
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
		t.Run(tc.description, func(t *testing.T) {
			db, engine := testDB(t, "CREATE TABLE records(id INTEGER PRIMARY KEY,payload BLOB)")
			_, err := engine.Prepare(t.Context(), PrepareRequest{Datasets: []Dataset{{Table: Table("records"), Source: maps(map[string]any{"id": 1, "payload": tc.input})}}})
			if err != nil {
				t.Fatal(err)
			}
			var storage string
			var actual any
			if err := db.QueryRow("SELECT typeof(payload),payload FROM records").Scan(&storage, &actual); err != nil {
				t.Fatal(err)
			}
			expected := tc.input
			if tc.expected == "null" {
				expected = nil
			}
			if storage != tc.expected || !reflect.DeepEqual(expected, actual) {
				t.Fatalf("storage=%s payload=%#v, expected %s %#v", storage, actual, tc.expected, expected)
			}
		})
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
