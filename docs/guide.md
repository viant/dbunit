# dbunit

SQL test preparation and validation using `database/sql`, SQLX, structology and toolbox data expansion. The caller owns the database handle. Fixture locations are explicit; test names and caller source paths never select files.

## Prepare and validate

Register your SQL driver and SQLX metadata product. For SQLite, for example:

```go
import (
    _ "modernc.org/sqlite"
    _ "github.com/viant/sqlx/metadata/product/sqlite"
)
// db, err := sql.Open("sqlite", ":memory:")
```

```go
engine, err := dbunit.New(db,
    dbunit.WithFixtures(fixture.Directory("/absolute/path/testdata")),
)
if err != nil { t.Fatal(err) }

report, err := engine.Run(ctx, dbunit.Case{
    State: data.Map{"customerID": "42"},
    Schema: []dbunit.Statement{{SQL: `CREATE TABLE customers (
        id INTEGER PRIMARY KEY, name TEXT, active BOOLEAN DEFAULT true
    )`}},
    Prepare: []dbunit.Dataset{{
        Name: "customers", Table: dbunit.Table("customers"),
        Source: fixture.File("customers.json"), Operation: dbunit.Replace,
    }},
    Act: func(ctx context.Context, run *dbunit.Run) error {
        _, err := run.Executor.ExecContext(ctx,
            "UPDATE customers SET name = ? WHERE id = ?", "Updated", 42)
        return err
    },
    Expect: []dbunit.Expectation{{
        Table: dbunit.Table("customers"), Scope: dbunit.Full,
        Expected: fixture.Maps([]map[string]any{{"id": 42, "name": "Updated"}}),
        Match: dbunit.MatchOptions{Keys: []string{"id"}},
    }},
})
dbtest.Require(t, report, err)
```

Fixture `customers.json`:

```json
[{"id":"$AsInt($customerID)","name":"$ToUpper(alice)"}]
```

Use the placeholder syntax appropriate to your driver in SQL you supply. Generated SQL quotes identifiers and binds data. `Table("schema", "table")` has two components; `Table("a.b")` names one identifier containing a dot.

## Explicit assets and embedded files

```go
//go:embed testdata
var assets embed.FS

engine, err := dbunit.New(db,
    dbunit.WithFixtures(fixture.Embed(&assets, "testdata")),
)
// fixture.File("customers.json") resolves inside this embed.FS.
```

`fixture.Embed` uses the AFS embed provider with the supplied filesystem on every open. `fixture.FS(fs, root)` accepts any `fs.FS`. `fixture.AFS(service, baseURL, options...)` supports explicitly configured AFS locations. `fixture.Directory` requires an absolute root and rejects escaping paths and symlinks.

Sources include `File`, `URL`, `Bytes`, `Maps`, and `Typed`. JSON and NDJSON preserve numbers with `json.Number`. CSV/TSV preserve text, including empty strings; `\N` represents SQL null (configurable via `Source.NullToken`). At the database boundary, numeric and boolean CSV strings are converted using column metadata; text such as `001` stays text in text columns. Dates/timestamps accept the documented layouts in `conversion.go`. Binary columns accept typed bytes or standard base64 strings, matching JSON snapshot encoding. Files are bounded to 64 MiB. `manifest.Load` reads strict version 1 JSON/YAML case manifests through the same resolver; an application can attach the Go `Act` callback afterward.

## Expansion and presence

Toolbox's predefined UDFs are installed for each run: `$AsInt(...)`, `$AsBool(...)`, `$AsFloat(...)`, `$AsJSON(...)`, string/time/collection functions and other `toolbox/data/udf` functions. Expansion occurs recursively in fixture fields before type conversion and SQL loading. A standalone UDF retains its native result type; embedded expressions become text. Variables come from engine, request/case and run state.

Custom UDFs can be supplied under `data.UDFKey` as `data.Map` or `map[string]any`, or directly in state using toolbox's function signature:

```go
state := data.Map{data.UDFKey: data.Map{
    "CustomerID": func(value any, state data.Map) (any, error) {
        return lookupCustomerID(value)
    },
}}
```

Custom entries override predefined names for that run. Registry maps and input fixtures are copied. Toolbox's own rules for unresolved expressions and UDF errors still apply; a failed function may leave an expression unexpanded. SQL type conversion can reject such a value, but a text column can retain it. Validate custom function inputs when this matters.

The generic adapter is `structology.Maps`. It converts maps to dynamic Go structs with `Has` markers, supports nested structures, and stores them back to maps. dbunit uses explicit present-column projections when inserting: absent values retain database defaults, explicit `nil` becomes SQL NULL, and zero/false/empty strings remain present. JSON database columns require `ColumnSpec{JSON: true}`; `Match.JSONColumns` decodes their actual values for structural comparison.

## Preparation and transactions

- `Insert`, `Replace`, `Upsert`, and `DeleteMatching` are explicit operations. Upsert and deletion require keys.
- Dataset `DependsOn` controls insertion order; replacement deletes in reverse dependency order.
- Default preparation uses an owned transaction, committing on success and rolling back on error.
- `WithTransaction(tx)` borrows a caller transaction without committing or rolling it back.
- `WithTransactionMode(CaseRollback)` on `Run` wraps preparation, Act and validation in a rollback transaction. Act must use `run.Executor` to participate. Schema setup precedes that transaction.
- `ExplicitNonAtomic` reports partial-write risk through the chosen transaction mode and result. Use separate requests for different database handles.
- Cleanup runs after the case, including on failure, with a bounded uncancelled context when the original context was cancelled.

`Batch` and the default `Auto` use SQLX insertion in bounded batches. `WithNativeLoad(NativeLoadOptions{...})` explicitly enables an installed SQLX loader after its transaction and format behavior has been checked for your driver. `Auto` can then select native loading above `MinRows`; `BulkRequired` fails if native loading is unavailable. Nontransactional native loaders require `ExplicitNonAtomic`. Register the driver and SQLX product/loader packages needed by your application.

Live native-loader tests pass on PostgreSQL 16 and MySQL 8, covering omitted/default values, nulls, zero/empty values, delimiter/newline text, owned rollback and caller transactions with one pooled connection. PostgreSQL native loading currently restricts table/column forms to those supported by its SQLX COPY loader; use batch loading for rejected identifier shapes. MySQL native loading treats server warnings as failures to prevent silent data coercion. BigQuery native loading remains unverified and requires explicit nontransactional opt-in.

## Expectations and diagnostics

Use explicit table or query expectations and one of `Full`, `Keys`, or `Query` scope. Keyed matching, explicitly ordered matching and duplicate-sensitive unordered matching are available. Unordered matching has no separate row-count cutoff. Literal fields narrow candidate rows through indexes; overlapping predicates use one-to-one augmenting-path matching, preserving duplicate multiplicity. Predicates without selective literal fields can require more comparisons. Queries have a default 10,000-row limit. Duplicate result column names require aliases.

Subset field comparison is the default; `StrictFields` also rejects extra fields. `Contains` allows extra rows. Empty key-scoped expectations require `AllowEmpty`; an empty full-table expectation asserts that the table is empty. Numeric comparison uses exact rational values rather than converting all integers to floating point. Existing `<ds:...>` assertion macros are supported; unsupported `@` directives fail explicitly.

A mismatch returns a typed `*AssertionError` and a `Report` with counts and paths. Operational failures remain errors. `dbtest.Require` connects this to Go's `testing.TB`.

`Exec` runs direct INSERT/UPDATE/DELETE/MERGE/REPLACE statements in one owned or caller transaction, expands bound arguments once, and rejects transaction-control/DDL statements before executing anything. Use `ApplySchema` for DDL. CTE-wrapped commands are currently rejected.

`Export` returns JSON/NDJSON fixture bytes from an explicit query. The caller chooses where to save them. `Compare` compares two explicit bounded queries, including named databases registered with `WithDatabase`.

## Schema load and extraction

`ApplySchema` accepts explicit SQL asset sources, statements, or an extracted artifact and delegates to `sqlx/io/schema`. `ExtractSchema` requires an explicit list of table names; dependency inclusion is opt-in. The artifact includes statements, completeness, omissions and a checksum. Partial artifacts are never silently treated as complete.

Schema round trips are tested on SQLite and in isolated PostgreSQL 16 and MySQL 8 servers. PostgreSQL extraction targets ordinary PostgreSQL 14+ tables; unsupported advanced features are reported as omissions. BigQuery native extraction remains partial and requires explicit acceptance of its omissions. Native bulk tests are described separately above. Applying schema and loading rows have different transaction guarantees, especially on databases with implicit DDL commits.

## Endly

Endly contains a thin `dbunit` service using this engine. It keeps the legacy `dsunit` service available. Connections and explicit fixture roots are session-bound; preparation and expectations delegate to dbunit. See `endly/service/testing/dbunit/README.md` for the implemented action contracts.

## Local development

Download the pinned dependencies and run the checks:

```sh
go mod download
GOWORK=off go test ./...
GOWORK=off go test -race ./...
CGO_ENABLED=0 GOWORK=off go test ./...
GOWORK=off go test -run '^$' -bench BenchmarkPrepareBatch -benchmem
```

The detailed design and remaining certification work are in [plan.md](../plan.md).
