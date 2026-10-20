# dbunit

**SQL database fixtures, preparation, and assertions for Go and Endly.**

`dbunit` helps test application behavior through the state of its database: load a known dataset, run the application, and verify the resulting rows. It uses Go's `database/sql`, [SQLX](https://github.com/viant/sqlx) for batch and native loading, and [toolbox/data](https://github.com/viant/toolbox/tree/master/data) for fixture expressions.

It is the SQL-focused successor to [dsunit](https://github.com/viant/dsunit). Assets are selected explicitly—through file roots, URLs, inline data, or `embed.FS`—and never inferred from test names or source-code locations.

## Contents

- [Features](#features)
- [Project status and installation](#project-status-and-installation)
- [Quick start](#quick-start)
- [Embedded fixtures](#embedded-fixtures)
- [Expressions and presence](#expressions-and-presence)
- [Hydration and sequences](#hydration-and-sequences)
- [Mapping logical fixtures](#mapping-logical-fixtures)
- [Verification policies](#verification-policies)
- [Transactions and loading](#transactions-and-loading)
- [Schema and snapshots](#schema-and-snapshots)
- [Endly](#endly)
- [Testing and contributing](#testing-and-contributing)
- [License and acknowledgements](#license-and-acknowledgements)

## Features

- Prepare rows with insert, replace, sparse upsert, and key-based deletion.
- Preserve the difference between an absent field, SQL `NULL`, and zero/false/empty values.
- Read JSON, NDJSON, CSV/TSV, typed Go slices, and maps from explicit assets.
- Expand toolbox variables and UDFs such as `$AsInt(...)`, including nested records.
- Generate fixture sequences and UUIDs, resolve references, and capture generated values.
- Project logical fixtures into related physical tables with dependency ordering and key-based deduplication.
- Compare full tables, selected keys, or explicit queries with precise numeric comparison and assertion predicates.
- Load and extract native schema, export query results as fixtures, and compare database queries.
- Use caller-owned database handles and explicit transaction policies.
- Integrate with Go tests and Endly workflows through the same core engine.

## Project status and installation

This repository is a **development version**; its API may evolve before a tagged release. Changes are tracked in [CHANGELOG.md](CHANGELOG.md). Use Go **1.25.8 or newer**, as declared in `go.mod`.

Install the module with:

```sh
go get github.com/viant/dbunit
```

Dependencies are pinned in `go.mod`. See [the implementation plan](plan.md) for remaining parity work.

From `dbunit`, build and test with:

```sh
GOWORK=off go test ./...
```

Register the SQL driver and corresponding SQLX metadata product in your application. Native loading additionally requires its SQLX loader registration. The library does not own or close your `*sql.DB`.

## Quick start

This example uses the pure-Go SQLite driver. PostgreSQL and MySQL use the same core API with their own drivers and SQL placeholder syntax.

```go
package example

import (
    "context"
    "database/sql"
    "testing"

    "github.com/viant/dbunit"
    "github.com/viant/dbunit/dbtest"
    "github.com/viant/dbunit/fixture"
    "github.com/viant/toolbox/data"
    _ "github.com/viant/sqlx/metadata/product/sqlite"
    _ "modernc.org/sqlite"
)

func TestCustomer(t *testing.T) {
    ctx := context.Background()
    db, err := sql.Open("sqlite", ":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    db.SetMaxOpenConns(1)

    engine, err := dbunit.New(db)
    if err != nil { t.Fatal(err) }

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
```

`Table("schema", "customer")` is a qualified identifier. `Table("customer.archive")` is one literal identifier containing a dot. Generated SQL quotes identifiers and binds data values.

## Embedded fixtures

```go
//go:embed testdata
var assets embed.FS

engine, err := dbunit.New(db,
    dbunit.WithFixtures(fixture.Embed(&assets, "testdata")),
)
// A dataset with fixture.File("customers.json") reads from this embed.FS.
```

`fixture.Embed` uses the AFS embed provider and retains the supplied filesystem for each read. Other resolvers include `fixture.Directory("/absolute/root")`, `fixture.FS(fsys, root)`, and `fixture.AFS(service, baseURL, options...)`.

Fixture paths are relative only to the declared root. `manifest.Load` reads strict versioned JSON/YAML case manifests through the same resolver. See [the usage guide](docs/guide.md) for formats, limits, and source options.

## Expressions and presence

```json
[
  {"id":"$AsInt($customerID)","name":"$ToUpper(alice)","active":"$AsBool(false)"}
]
```

Standalone UDF expressions retain their result type. Embedded expressions become text. Each run has its own registry of predefined toolbox UDFs; caller functions can override or extend it through `data.UDFKey`.

| Fixture field | Meaning |
|---|---|
| Field absent | Omit the column and retain its database default |
| `null` | Write SQL `NULL` |
| `0`, `false`, `""` | Write that explicit value |

The underlying `structology.Maps` adapter preserves nested presence through dynamic Go structs and `Has` markers. Hydration and mapping return `fixture.Source` values marked `expanded: true`; preparation does not evaluate their strings again. `fixture.Resolved(rows)` provides the same boundary for already-concrete data.

Toolbox's own error behavior is preserved: an unresolved or failed expression can remain literal text. Invalid typed column values fail conversion, but text columns can retain that text. See [the usage guide](docs/guide.md) for custom UDFs and conversion rules.

## Hydration and sequences

Hydration builds concrete fixture rows before insertion. It supports named local sequences, ordered generation rules, UUIDs, tags/references, and captures. Sequence seeds can come from explicit database queries; a seed is a snapshot, not a concurrent database reservation.

Hydration does not insert records. Its returned identifiers and captures describe planned fixtures until preparation succeeds. The API and examples are detailed in [fixture generation and mappings](docs/fixtures.md).

## Mapping logical fixtures

A mapping projects one logical fixture into multiple physical table datasets. It supports nested and repeated source paths, parent/root references, literal defaults, required fields, explicit composite keys, and table dependencies.

Repeated keys merge complementary absent fields; conflicting present values fail. Explicit null, zero and empty values remain distinct from absence. Mapping outputs are concrete datasets ready for `Prepare`.

See [fixture generation and mappings](docs/fixtures.md) for a complete parent/child example and the exact path and conflict rules.

## Verification policies

Dbunit preserves the two SQL verification scopes used by dsunit:

| dsunit | dbunit | Rows checked |
|---|---|---|
| `FullTableDatasetCheckPolicy` | `Scope: dbunit.Full` | The whole table; extra or missing rows fail by default |
| `SnapshotDatasetCheckPolicy` | `Scope: dbunit.Keys` with `Match.Keys` | Rows selected by the expected key tuples; unrelated rows are ignored |

```go
expectation := dbunit.Expectation{
    Table: dbunit.Table("customer"),
    Scope: dbunit.Keys,
    Match: dbunit.MatchOptions{Keys: []string{"id"}},
    Expected: fixture.Maps([]map[string]any{{"id": 42, "name": "Updated"}}),
}
```

Snapshot keys are explicit and can be composite. Missing selected rows or duplicate actual keys fail. An empty full-table expectation asserts that the table is empty; an empty snapshot requires `AllowEmpty` because it selects no rows.

[Assertly](https://github.com/viant/assertly) evaluates predicate assertions and `<ds:...>` macros. Dbunit controls row selection, one-to-one matching, exact numeric comparison, and failure aggregation. Expected fields are a subset by default; `StrictFields` also rejects extra fields. `Contains` explicitly allows additional rows within the selected scope. These options are independent of full-table versus snapshot selection.

Legacy `@` directives are not silently interpreted; supported matching options are explicit fields on `MatchOptions`.

## Transactions and loading

Default preparation owns one transaction, commits on success, and rolls back on error. `WithTransaction(tx)` borrows a caller transaction without completing it. `CaseRollback` wraps preparation, the application callback and assertions in a transaction that is rolled back; the callback must use `run.Executor`. Schema setup precedes that transaction.

`Batch` and default `Auto` use SQLX batches. `WithNativeLoad` explicitly enables a registered native loader with its transaction/format capabilities. `BulkRequired` fails when its requirements cannot be met. Nontransactional loaders require `ExplicitNonAtomic`; failed native writes are never retried as a batch.

| Dialect | Verification and limits |
|---|---|
| SQLite | Local schema, preparation, assertion, transaction, and workflow tests |
| PostgreSQL | Live PostgreSQL 16 schema/native-load tests; schema extractor targets ordinary PostgreSQL 14+ tables |
| MySQL | Live MySQL 8 schema/native-load tests; native server warnings fail the load |
| BigQuery | Nested adaptation covered locally; live loading/schema completeness remains unverified |

PostgreSQL native COPY currently restricts identifier shapes; use batch loading for unsupported cases. Advanced schema objects are supported only where documented by SQLX. See [the usage guide](docs/guide.md) for matching limits and transaction details.

## Schema and snapshots

- `ApplySchema`: apply explicit SQL files, statements, or extracted artifacts.
- `ExtractSchema`: extract selected tables with dependency and completeness policies.
- `Export`: return JSON/NDJSON fixtures from an explicit bounded query.
- `Compare`: compare two explicit queries, including named databases.
- `Exec`: execute direct DML statements with bound-argument expansion and transaction ownership.

Schema artifacts include omissions and a checksum. Incomplete exports require explicit acceptance. Extraction is a same-dialect test-schema operation, not a full database backup or universal migration tool.

## Endly

The Endly `dbunit` service exposes the core operations through workflow actions and MCP. Database pools and fixture resolvers are session-scoped; legacy `dsunit` remains available during migration. Inline fixtures and query arguments are deferred to the core so stateful expressions run once.

Compose `sequence`, `hydrate`, `map`, and `prepare` using explicit captures and state selectors. Seed each counter from its named database binding; bind each child to its parent through a tag reference or `MappingParentRow`. See [cross-database counters and parent/child IDs](docs/fixtures.md#cross-database-counters-and-parentchild-ids). Preparation is atomic within one database; separate database actions do not share a distributed transaction.

See the Endly checkout's `service/testing/dbunit/README.md` for action contracts, native loading options, captures, and embedding APIs. The new fixture-generation API does not replace or modify the legacy global `AsTableRecords` UDF.

## Testing and contributing

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
CGO_ENABLED=0 GOWORK=off go test ./...
```

Native PostgreSQL/MySQL tests are opt-in. See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, test-service requirements, and benchmark commands. Bug reports and pull requests should include a small reproducible fixture and database/driver versions.

## License and acknowledgements

Licensed under the [Apache License, Version 2.0](LICENSE). Attribution is retained in [NOTICE](NOTICE); both files were copied from dsunit.

This project builds on Viant's [dsunit](https://github.com/viant/dsunit), [SQLX](https://github.com/viant/sqlx), [structology](https://github.com/viant/structology), [toolbox](https://github.com/viant/toolbox), [AFS](https://github.com/viant/afs), and [assertly](https://github.com/viant/assertly).
