package example

import (
	"context"
	"database/sql"
	"testing"

	"github.com/viant/dbunit"
	"github.com/viant/dbunit/dbtest"
	"github.com/viant/dbunit/fixture"
	_ "github.com/viant/sqlx/metadata/product/sqlite"
	"github.com/viant/toolbox/data"
	_ "modernc.org/sqlite"
)

func TestCustomer(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	engine, err := dbunit.New(db)
	if err != nil {
		t.Fatal(err)
	}

	report, err := engine.Run(ctx, dbunit.Case{
		State: data.Map{"customerID": "42"},
		Schema: []dbunit.Statement{{SQL: `CREATE TABLE customer (
            id INTEGER PRIMARY KEY, name TEXT, active BOOLEAN DEFAULT true
        )`}},
		Prepare: []dbunit.Dataset{{
			Table: dbunit.Table("customer"),
			Source: fixture.Maps([]map[string]any{{
				"id": "$AsInt($customerID)", "name": "Alice",
			}}),
		}},
		Act: func(ctx context.Context, run *dbunit.Run) error {
			_, err := run.Executor.ExecContext(ctx,
				"UPDATE customer SET name = ? WHERE id = ?", "Updated", 42)
			return err
		},
		Expect: []dbunit.Expectation{{
			Table: dbunit.Table("customer"),
			Scope: dbunit.Full,
			Expected: fixture.Maps([]map[string]any{{
				"id": 42, "name": "Updated", "active": true,
			}}),
			Match: dbunit.MatchOptions{Keys: []string{"id"}},
		}},
	})
	dbtest.Require(t, report, err)
}
