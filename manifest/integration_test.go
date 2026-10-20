package manifest_test

import (
	"context"
	"database/sql"
	"github.com/viant/dbunit"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/dbunit/manifest"
	_ "github.com/viant/sqlx/metadata/product/sqlite"
	_ "modernc.org/sqlite"
	"testing"
	"testing/fstest"
)

func TestManifestSchemaPreparationAndUDF(t *testing.T) {
	assets := fstest.MapFS{
		"cases/example.yaml": {Data: []byte(`version: 1
name: explicit-assets
state:
  id: "41"
schema:
  - sql: CREATE TABLE customer(id INTEGER PRIMARY KEY, name TEXT)
prepare:
  - table: customer
    source:
      path: fixtures/customer.json
expect:
  - table: customer
    expected:
      kind: maps
      data:
        - id: "$AsInt($id)"
          name: ALICE
    match:
      keys: [id]
`)},
		"fixtures/customer.json": {Data: []byte(`[{"id":"$AsInt($id)","name":"$ToUpper(alice)"}]`)},
	}
	resolver := fixture.FS(assets, ".")
	ctx := context.Background()
	definition, err := manifest.Load(ctx, resolver, fixture.File("cases/example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	engine, err := dbunit.New(db, dbunit.WithFixtures(resolver))
	if err != nil {
		t.Fatal(err)
	}
	report, err := engine.Run(ctx, definition)
	if err != nil {
		t.Fatalf("%v: %+v", err, report)
	}
	if report.Failed != 0 || report.Passed == 0 {
		t.Fatalf("%+v", report)
	}
}
