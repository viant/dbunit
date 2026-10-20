package dbunit

import (
	"context"
	"database/sql"
	"fmt"
	mysqlDriver "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/viant/sqlx/metadata/product/mysql"
	_ "github.com/viant/sqlx/metadata/product/mysql/load"
	_ "github.com/viant/sqlx/metadata/product/pg"
	_ "github.com/viant/sqlx/metadata/product/pg/load"
	"os"
	"testing"
	"time"
)

// These tests use an explicit service selected by the caller. They create and
// remove unique objects; neither default credentials nor a default host exists.
func TestNativeLive(t *testing.T) {
	for _, vendor := range []struct{ dialect, driver, env string }{{"postgres", "postgres", "DBUNIT_TEST_POSTGRES_DSN"}, {"mysql", "mysql", "DBUNIT_TEST_MYSQL_DSN"}} {
		t.Run(vendor.dialect, func(t *testing.T) {
			dsn := os.Getenv(vendor.env)
			if dsn == "" {
				t.Skip(vendor.env + " is not configured")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			db, err := sql.Open(vendor.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.PingContext(ctx); err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("dbunit_native_%d", time.Now().UnixNano())
			if vendor.dialect == "mysql" {
				config, err := mysqlDriver.ParseDSN(dsn)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
					t.Fatal(err)
				}
				defer db.ExecContext(context.Background(), "DROP DATABASE "+name)
				config.DBName = name
				handle, err := sql.Open("mysql", config.FormatDSN())
				if err != nil {
					t.Fatal(err)
				}
				defer handle.Close()
				db = handle
			}
			table := name
			if vendor.dialect == "mysql" {
				table = "records"
			}
			e, err := New(db, WithDialect(vendor.dialect), WithNativeLoad(NativeLoadOptions{Transactional: true}))
			if err != nil {
				t.Fatal(err)
			}
			quoted, _ := e.quote(Table(table))
			if _, err := db.ExecContext(ctx, "CREATE TABLE "+quoted+"(id BIGINT PRIMARY KEY,label VARCHAR(100) DEFAULT 'default',qty BIGINT DEFAULT 9)"); err != nil {
				t.Fatal(err)
			}
			defer db.ExecContext(context.Background(), "DROP TABLE "+quoted)
			db.SetMaxOpenConns(1)
			rows := []map[string]any{{"id": 1}, {"id": 2, "label": nil, "qty": nil}, {"id": 0, "label": "", "qty": 0}, {"id": 3, "label": "null", "qty": 3}, {"id": 4, "label": "edge,*#^\ntext", "qty": 4}, {"id": 5, "label": "^N", "qty": 5}}
			result, err := e.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table(table), Source: maps(rows...), Load: LoadOptions{Strategy: BulkRequired}}}})
			if err != nil {
				t.Fatalf("native prepare: %v %+v", err, result)
			}
			expected := []map[string]any{{"id": 1, "label": "default", "qty": 9}, {"id": 2, "label": nil, "qty": nil}, {"id": 0, "label": "", "qty": 0}, {"id": 3, "label": "null", "qty": 3}, {"id": 4, "label": "edge,*#^\ntext", "qty": 4}, {"id": 5, "label": "^N", "qty": 5}}
			report, err := e.Expect(ctx, ExpectRequest{Expectations: []Expectation{{Table: Table(table), Expected: maps(expected...), Match: MatchOptions{Keys: []string{"id"}}}}})
			if err != nil {
				t.Fatalf("native values: %v %+v", err, report)
			}
			// A later duplicate must roll back an earlier successful native load.
			_, err = e.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table(table), Source: maps(map[string]any{"id": 10}), Load: LoadOptions{Strategy: BulkRequired}}, {Table: Table(table), Source: maps(map[string]any{"id": 0}), Load: LoadOptions{Strategy: BulkRequired}}}})
			if err == nil {
				t.Fatal("duplicate key did not fail")
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+quoted+" WHERE id=10").Scan(&count); err != nil || count != 0 {
				t.Fatal("rollback", count, err)
			}
			// The supplied transaction must remain open and caller-owned.
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Prepare(ctx, PrepareRequest{Datasets: []Dataset{{Table: Table(table), Source: maps(map[string]any{"id": 20}), Load: LoadOptions{Strategy: BulkRequired}}}}, WithTransaction(tx))
			if err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+quoted+" WHERE id=20").Scan(&count); err != nil || count != 0 {
				t.Fatal("caller rollback", count, err)
			}
		})
	}
}
