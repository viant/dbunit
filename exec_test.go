package dbunit

import (
	"context"
	"github.com/viant/toolbox/data"
	"testing"
)

func TestExecOwnershipAndUDF(t *testing.T) {
	db, engine := testDB(t, `CREATE TABLE executed(id INTEGER PRIMARY KEY)`)
	ctx := context.Background()
	calls := 0
	state := data.Map{"Next": func(any, data.Map) (any, error) { calls++; return calls, nil }}
	result, err := engine.Exec(ctx, ExecRequest{State: state, Statements: []Statement{{SQL: "INSERT INTO executed VALUES(?)", Args: []any{"$Next(0)"}}}})
	if err != nil || result.Transaction != "committed" || calls != 1 || result.RowsAffected != 1 {
		t.Fatalf("%+v %v %d", result, err, calls)
	}
	result, err = engine.Exec(ctx, ExecRequest{Statements: []Statement{{SQL: "INSERT INTO executed VALUES(2)"}, {SQL: "INSERT INTO executed VALUES(1)"}, {SQL: "INSERT INTO executed VALUES(3)"}}})
	if err == nil || result.Transaction != "rolledBack" || result.Statements != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM executed").Scan(&count); err != nil || count != 1 {
		t.Fatalf("%d %v", count, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err = engine.Exec(ctx, ExecRequest{Statements: []Statement{{SQL: "INSERT INTO executed VALUES(2)"}}}, WithTransaction(tx))
	if err != nil || result.Transaction != "callerOwned" {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err = tx.Exec("INSERT INTO executed VALUES(3)"); err != nil {
		t.Fatal("caller transaction was closed", err)
	}
}
func TestExecRejectsOwnershipStatementsBeforeWrites(t *testing.T) {
	_, engine := testDB(t, `CREATE TABLE executed(id INTEGER PRIMARY KEY)`)
	for _, sqlText := range []string{"COMMIT", "WITH x AS (SELECT 1) SELECT * FROM x", "CREATE TABLE hidden(id INT)", "INSERT INTO executed VALUES(1); COMMIT"} {
		result, err := engine.Exec(context.Background(), ExecRequest{Statements: []Statement{{SQL: "INSERT INTO executed VALUES(9)"}, {SQL: sqlText}}})
		if err == nil || result.Statements != 0 {
			t.Fatalf("%q %+v %v", sqlText, result, err)
		}
	}
}
