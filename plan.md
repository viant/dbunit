# dbunit: implementation plan

Status: proposed implementation specification. No dbunit implementation is included in this change.

Date: 2026-10-20.

Target: `github.com/viant/dbunit` (`github.com/viant/dbunit`).

## 1. Purpose and agreed decisions

Build a SQL datastore preparation and validation framework that can replace dsunit's SQL testing workflows. Database access uses Go's `database/sql`. Fixture insertion and bulk loading use `github.com/viant/sqlx`. Preserve the powerful `github.com/viant/toolbox/data` expansion engine and existing assertly predicates where practical.

The user explicitly selected these design directions:

1. Accept caller-owned `*sql.DB`; remove the dependency on dsc managers and datastore registries.
2. Make fixture locations explicit. Never derive paths, table names, case names, or prefixes from Go test names, runtime callers, or stack frames.
3. Support `go:embed` / `embed.FS`, including the AFS embed provider, as a primary fixture source.
4. Adapt `[]map[string]any` into runtime Go structs built with `reflect.StructOf`, with cached accessors and mutators.
5. Include a `Has` presence marker in each generated struct so absent, present-null, and present-zero values remain distinguishable.
6. Put the reusable adapter in **structology**, not dbunit. The accepted facade name is **`Maps`**, with **`Load`** and **`Store`** for both directions.
7. Support recursive nested objects and repeated structures, including BigQuery RECORD/STRUCT and repeated-record use cases. Do not flatten them by default.

The replacement is behavioral, not a source-compatible rename. Preserve dsunit capabilities within the SQL scope, offer an explicit fixture migration layer, and replace ambiguous or unsafe semantics with documented policies.

## 2. Source inspection and evidence

This plan is based on local source inspection, not assumptions about a future dependency release. Inspected checkouts included dsunit `dbf3d8d`, sqlx `2b47eeb`, afs `90e95d4`, and toolbox `da0b3fd`. Implementation must pin dependency versions that actually contain the required APIs and verify any changes since this inspection.

| Source | Observation | Design consequence |
| --- | --- | --- |
| `../dsunit/service.go` | Prepare, Expect, query, freeze, dump, compare, schema checks and sequence logic live in one service. Preparation shares mutable error/context state. | Separate compilation, execution, loading, and validation; each run owns mutable state. |
| `../dsunit/dataset.go`, `tester.go` | Fixture discovery, filename conventions and control records are intertwined. | Explicit source selection, manifest entries and policies. |
| `../sqlx/io/load/service.go` | `load.New(ctx, db, table)` and `Exec(ctx, rows, ...loption.Option)` dispatch to a registered vendor loader. | Use SQLX loaders through a narrow internal adapter. |
| `../sqlx/loption/option.go` | `WithTransaction`, `WithFormat`, `WithHint`, `WithCommonOptions` exist. | Options are available, but individual vendor behavior must be certified. |
| `../sqlx/io/insert/service.go` | Batch insertion accepts typed rows and options, including a caller transaction through SQLX transaction handling. | Use as the portable SQLX insertion strategy. |
| `../sqlx/io/iter.go` | SQLX consumes typed slices through `Values`. | A dynamically constructed `[]*T` is a suitable representation, subject to integration tests. |
| `../sqlx/io/insert/presence_sqlite_test.go` | `Has *flags` with `sqlx:"-" setMarker:"true"` protects explicit zero identity values. | Follow this marker convention; do not assume it implements all sparse bulk-column behavior. |
| `../sqlx/metadata/product/pg/load/session.go` | Loader derives struct columns, lowercases column names, and uses `pq.CopyIn`. | Quoted mixed-case and schema-qualified targets require capability tests or SQLX fixes. |
| `../sqlx/metadata/product/mysql/load/session.go` | Uses LOAD DATA LOCAL INFILE with a registered reader and optional transaction. Has an affected-row fallback. | Verify local-infile availability, SQL warnings, transaction behavior and row counts. |
| `../sqlx/metadata/product/bigquery/load/session.go` | Supports CSV, JSON and Parquet, executes through `db.ExecContext`, and does not enlist a supplied transaction. | BigQuery native load must not be advertised as caller-transaction atomic. |
| `../sqlx/io/load/reader/json/reader.go` | Uses standard `encoding/json` for rows. | `Has` is not automatically respected during JSON load serialization. An explicit presence-aware bridge is required. |
| `../afs/embed/manager.go`, `open.go`, `list.go` | Embed FS can be supplied as storage options. OpenURL can fall back to OS access if the FS is not provided. | Bind and forward the FS on every operation; verify no unintended disk fallback. |
| `../afs/service.go` | `NewWithProviders` supports private providers. | Keep fixture roots private to a resolver; avoid global embed registration and cross-test collisions. |
| `../structology/state.go`, `selector.go`, `marker.go` | State types, field accessors/mutators and marker operations already exist. Nil marker holders can mean all fields present. | Reuse these mechanisms and allocate a marker holder for every adapted row and nested object. |
| `../toolbox/data/map.go` | Typed expansion, nested paths, collections, spreads and UDFs; unresolved expressions may remain unchanged. | Retain the engine and define explicit unresolved-expression policy. |
| `../assertly/context.go`, `validator.go` | `AssertWithContext` accepts a custom context/evaluator. | Use private evaluators rather than registering dbunit macros globally. |

The inspected SQLX checkout declares Go 1.25.8. Choose dbunit's Go version from the dependency set actually pinned; do not repeat dsunit's old Go 1.10 compatibility claim. A workspace build alone is insufficient: verify `GOWORK=off` with published or explicitly pinned module revisions before release.

## 3. Capability parity and boundaries

| dsunit capability | dbunit replacement | Delivery |
| --- | --- | --- |
| Register datastore | `New(db, ...)`; optional explicitly named additional `*sql.DB` bindings | Core |
| Init / recreate / schema scripts | Explicit schema steps, optional admin binding, capability-checked recreation | Core scripts; recreation extension |
| Prepare JSON/CSV/TSV/NDJSON | Explicit source + table + operation; SQLX loading | Core |
| Inline data | Maps and typed Go slices | Core |
| Full-table expectation | Exact row-set comparison for explicitly selected tables | Core |
| Snapshot expectation | Key-scoped comparison or explicit query scope | Core |
| Macro values and predicates | Toolbox expansion + private assertly evaluator | Core |
| Sequences / generated IDs | Explicit identities, generated-key capture, dialect sequence operations | Core identities; dialect features phased |
| Table/view mapping | Explicit reusable mapping graph, deterministic expansion | SQL parity phase |
| Query | Bound SQL + optional capture and assertions | Core |
| Export | Explicit query export with transformations and presence-aware serialization | SQL parity phase |
| Dump DDL / vendor type mapping | Same-dialect extraction for e2e first; explicit cross-vendor mappings later | Initial release; conversion in SQL parity phase |
| Compare databases | Two explicit queries with key/matching/normalization options | SQL parity phase |
| Check schema | Normalized schema descriptors and structured differences | SQL parity phase |
| Ping / readiness | Context-aware bounded retries over `PingContext` | Core utility |
| HTTP client/server | Optional separate transport over the same contracts, if consumers need it | Final SQL parity phase |
| Go test convenience functions | Small helper around error/report APIs, accepting `testing.TB` | Core |
| Test-name discovery | Deliberately removed | Migration must replace with explicit paths |
| NoSQL managers | Outside this database/sql library | No emulation of dsc's NoSQL layer |

BigQuery remains in scope through its database/sql driver and SQLX loader. SQL-only scope does not mean relational, flat rows only. Nested data validation and nested map adaptation are first-class requirements.

A complete SQL replacement is not declared at the first insert/expect milestone. Publish a parity checklist showing which utilities, dialects, macros and directives are supported, pending, or deliberately changed.

## 4. Architecture and ownership

```text
Explicit Case / standalone Prepare / standalone Expect
                          |
                 resolve and decode assets
                          |
              compile immutable execution plan
                          |
          per-run toolbox state + private macro context
                          |
              mapping and typed value expansion
                          |
       structology.Maps.Load -> []*dynamicRow with Has
                          |
             presence-aware load strategy selection
                  /                    \
         sqlx batch insert         sqlx native load
                  \                    /
                    database/sql
                          |
           SQL reads -> normalized actual records
                          |
             row matching + assertly value checks
                          |
                structured report + error
```

Ownership rules:

- **structology** owns generic map/struct conversion, runtime type construction, field-name mapping, recursive presence and compiled field access.
- **dbunit** owns test specifications, SQL metadata interpretation, fixture sources, expansion sequencing, execution policy, validation, reporting and migration.
- **sqlx** owns SQL insertion/loading and vendor integrations. Fix loader limitations there rather than duplicating a native loader inside dbunit.
- **toolbox/data** owns expression evaluation. Dbunit wraps its lifecycle and diagnostics without introducing a competing expression language.
- **assertly** supplies predicate/value comparison where its semantics match the dbunit contract. Dbunit controls row selection, exact cardinality, key identity and aggregation.
- Callers own database handles and their lifetime. Dbunit never closes a supplied `*sql.DB` or completes a supplied `*sql.Tx`.

An Engine is reusable immutable configuration plus bounded synchronized metadata/adapter caches. A Run owns state, transactions, mutable buffers, generated values, error aggregation and reports. A Plan contains reusable immutable instructions; it never holds a live transaction, mutable fixture maps or a captured caller context.

## 5. Proposed public API

Names below are a design target, not existing dbunit symbols. Compile examples as part of the implementation and update this document when the actual public API is settled.

```go
func New(db *sql.DB, options ...Option) (*Engine, error)

func (e *Engine) Compile(ctx context.Context, c Case) (*Plan, error)
func (e *Engine) Run(ctx context.Context, c Case, options ...RunOption) (*Report, error)
func (e *Engine) RunPlan(ctx context.Context, p *Plan, options ...RunOption) (*Report, error)
func (e *Engine) Prepare(ctx context.Context, r PrepareRequest, options ...RunOption) (*PrepareResult, error)
func (e *Engine) Expect(ctx context.Context, r ExpectRequest, options ...RunOption) (*Report, error)
func (e *Engine) Query(ctx context.Context, r QueryRequest, options ...RunOption) (*QueryResult, error)

// Supporting contracts; exact field spelling is provisional.
type Case struct {
    Name    string                 // diagnostics only; never determines paths
    State   data.Map               // copied into run-owned state
    Schema  []Statement
    Prepare []Dataset
    Act     func(context.Context, *Run) error
    Expect  []Expectation
    Cleanup []Statement
}

type Dataset struct {
    Name      string
    Database  string               // empty selects the engine's default DB
    Table     Identifier
    Source    fixture.Source
    Operation Operation            // Insert, Replace, Upsert, DeleteMatching
    Load      LoadOptions
    Columns   []ColumnSpec          // optional explicit schema overrides
    Keys      []string
    DependsOn []string
    Capture   *CaptureSpec
}

type Expectation struct {
    Name      string
    Database  string
    Table     Identifier           // mutually exclusive with Query
    Query     *QuerySpec
    Expected  fixture.Source
    Scope     Scope                // Full, Keys, Query
    Match     MatchOptions         // keys/order/multiset, field policy, limits
}
```

Make invalid combinations fail validation before execution: table plus query, multiple source variants, upsert without keys, ordered matching without an explicit order contract, unresolved database name, duplicate dataset IDs, invalid cleanup scope, or an unsupported transaction/load combination.

Standalone operations support the common pattern where application logic runs between Prepare and Expect. `Run` is a convenience orchestration API, not a requirement to place application logic in configuration.

Errors are idiomatic Go errors. An expectation mismatch returns a report **and** an `AssertionError`; operational errors retain their causes with `Unwrap`. `Report.Err()` is consistent with the returned error. Never require callers to inspect a string status to discover failure.

A `dbtest` package can offer `Require(t testing.TB, report *Report, err error)` and preparation helpers. It must call `t.Helper()`, handle nil reports safely and avoid printing secrets. The core package must not require `*testing.T`.

### 5.1 Embedded example

```go
// Proposed dbunit API; implement as a compilable example.
//go:embed testdata/orders
var assets embed.FS

func TestCreateOrder(t *testing.T) {
    ctx := context.Background()
    db := openTestDatabase(t) // application test helper owns lifetime

    engine, err := dbunit.New(db,
        dbunit.WithDialect("postgres"),
        dbunit.WithFixtures(fixture.Embed(&assets, "testdata/orders")),
    )
    if err != nil { t.Fatal(err) }

    report, err := engine.Run(ctx, dbunit.Case{
        Name: "create order",
        State: data.Map{"customerID": int64(101)},
        Prepare: []dbunit.Dataset{
            {
                Name: "customers",
                Table: dbunit.Table("customers"),
                Source: fixture.File("prepare/customers.json"),
                Operation: dbunit.Replace,
            },
        },
        Act: func(ctx context.Context, run *dbunit.Run) error {
            id, err := createOrder(ctx, db, 101)
            if err != nil { return err }
            return run.Set("orderID", id)
        },
        Expect: []dbunit.Expectation{
            {
                Name: "created order",
                Table: dbunit.Table("orders"),
                Expected: fixture.File("expect/orders.json"),
                Scope: dbunit.Keys,
                Match: dbunit.MatchOptions{Keys: []string{"id"}},
            },
        },
    })
    dbtest.Require(t, report, err)
}
```

Expected JSON can contain `{"id":"$orderID","customer_id":"$customerID"}`. It is expanded after Act, so it sees the captured ID. Source paths are explicit relative to a declared fixture root; they are never relative to a discovered test source directory. This example uses committed preparation so the application's own database operations can see the fixtures.

### 5.2 Explicit manifests

Support versioned JSON and YAML manifests containing serializable Case fields. Go callbacks are supplied in code; they are not serialized as executable text.

```yaml
version: 1
name: create-order
prepare:
  - name: customers
    table: customers
    source: prepare/customers.json
    operation: replace
    load:
      strategy: auto
expect:
  - name: created-order
    table: orders
    source: expect/orders.json
    scope: keys
    keys: [id]
```

A manifest is itself selected explicitly, e.g. `fixture.File("cases/create-order.yaml")`. References resolve against a documented root, not whichever working directory happens to be active. Recommended default: all manifest paths are relative to the resolver root; an explicit manifest-directory base is an option, never implicit guessing. Unknown manifest fields and unsupported versions are errors.

## 6. Fixture sources and AFS embed

Implement a small Source discriminated union: File, URL, Maps, Typed, Bytes and ReaderFactory. Exactly one source is allowed per dataset or expectation. A reader factory is repeatable; a one-shot reader cannot be used in a reusable Plan without explicit buffering.

Resolver entry points:

- `fixture.Embed(&assets, "testdata/orders")`: wraps AFS embed with a bound FS and explicit root.
- `fixture.FS(fsys, "orders")`: general `io/fs.FS` support for `fstest.MapFS`, `fs.Sub`, and caller-owned virtual filesystems.
- `fixture.Directory(absoluteRoot)`: explicit local root; require an absolute root to remove hidden cwd dependency.
- `fixture.AFS(service, baseURL, options...)`: caller-provided AFS service and explicitly retained options.
- `fixture.URL("s3://bucket/path/users.json")`: explicit remote asset; support only configured schemes and credentials.
- `fixture.Maps(rows)` and `fixture.Typed(rows)`: in-memory inputs.

AFS embed implementation requirements:

1. Bind the specific `embed.FS` to the resolver; do not register test-specific instances in global scheme state.
2. Forward the FS option for list, open, and download. The inspected embed OpenURL must not accidentally fall back to `os.Open`.
3. Use a resolver-private AFS service/provider or direct bound manager operations. Private factories must retain the bound FS even when call options are absent.
4. Validate paths with slash semantics and `fs.ValidPath`; reject traversal outside the declared root. Deal with `.` as the root explicitly.
5. Two independent embedded filesystems containing the same path must work concurrently without content leakage.
6. Embed is read-only. Export and dump require a separate writable destination; never try to upload into embedded assets.
7. Cache keys include resolver identity plus path, format and content identity. Do not cache only by URL text.

Directories and globs are convenience features only when explicitly requested. A directory manifest must map files to tables, or an explicit option must authorize filename-to-table mapping. Sort discovery deterministically, reject duplicate targets unless ordered multiple operations are explicitly declared, and fail when a required source matches zero files.

### Decoding and source diagnostics

- JSON arrays and NDJSON: use `json.Decoder.UseNumber`; retain integer and decimal precision. Preserve nested arrays/maps.
- CSV/TSV: use a real CSV reader with configurable delimiter, multiline quoted values, BOM handling, explicit null token, duplicate-header checks and field counts. Do not split records with `bufio.Scanner`.
- CSV empty string, null token and missing column have distinct meanings. A row with fewer fields is malformed by default. The header defines presence for successfully decoded cells.
- Formats with no schema must use column specs/DB metadata or deterministic complete-batch inference. Never infer types from only the first row.
- Define row/byte/depth limits, with meaningful errors rather than silent truncation.
- Preserve source URL, dataset name, row number, column or nested path through all conversion errors.
- Typed sources retain their type and markers where possible; do not stringify them through JSON for convenience.
- Empty input is a valid empty dataset only when explicitly supplied. Missing/empty files are parse errors unless the format expressly represents an empty collection.

## 7. Structology Maps adapter: separately owned prerequisite

Implementation location: `github.com/viant/structology`.

Delegated visible chat: `01a15981-2bda-7c43-be29-e5045ecf61b9`, titled “Add bidirectional map and struct adapter to structology”. The delegated chat has delivered an initial implementation in structology/maps.go, tests in maps_test.go, and README examples. It reports passing `GOWORK=off go test -race ./...` and `go vet ./...`. The following public names were checked against that implementation. SQLX and BigQuery end-to-end certification remains part of the dbunit phases; generic adapter test success alone does not establish it.

### 7.1 Public facade and direction

```go
type Maps []map[string]any

// Maps -> supplied typed destination, or dynamically generated typed slice.
func (m Maps) Load(dest any, options ...MapOption) error

// Struct(s)/typed slice -> maps, consulting presence markers recursively.
func (m *Maps) Store(src any, options ...MapOption) error
```

Implemented plan APIs include `NewMapPlan([]MapField)`, `InferMapPlan(Maps)`, `NewMapPlanForType(reflect.Type)`, `WithMapFields(...)`, and `WithMapPlan(...)`. `MapField` supplies `Key`, `Name`, `Type`, `Tag`, nested `Fields`, and `Repeated`. `MapPlan.Load` and `MapPlan.Store` expose direct plan reuse; `Type` and `SliceType` expose the generated runtime types. The initial implementation defaults generated tags to JSON and SQLX spellings; dbunit should supply explicit tags for database-specific mappings rather than depending on inferred names.

For dynamic use, `var rows any; err := maps.Load(&rows, ...)` produces a homogeneous runtime `[]*T` (or another documented typed slice representation) behind `any`. It must not return `[]any` containing unrelated row types. For supplied types, callers pass `&[]*Order{}` or another supported destination shape.

Provide a reusable compiled mapping plan for hot paths. The facade may compile/cache internally, but dbunit must be able to compile once per schema and load many batches without rebuilding accessors or reflect types. Generic schema descriptors contain keys, Go field names, Go types, tags, nullability, nested fields and collection element schemas; they contain no SQL dialect or database imports.

`Load` and `Store` must leave destinations unchanged on failure unless an explicitly documented streaming API permits partial output. `Store` replaces the receiver by default; appending is an explicit option. Caller maps and source structs are not mutated.

### 7.2 Generated representation

Conceptual generated shape (anonymous runtime types are produced by `reflect.StructOf`):

```go
type Order struct {
    ID       *int64      `json:"id" sqlx:"id"`
    Active   *bool       `json:"active" sqlx:"active"`
    Profile  *Profile    `json:"profile" sqlx:"profile"`
    Items    []*Item     `json:"items" sqlx:"items"`
    Has      *OrderHas   `json:"-" sqlx:"-" setMarker:"true"`
}
type OrderHas struct { ID, Active, Profile, Items bool }
type Profile struct {
    City *string     `json:"city" sqlx:"city"`
    Has  *ProfileHas `json:"-" sqlx:"-" setMarker:"true"`
}
type ProfileHas struct { City bool }
```

Pointer/null representations are selected by the schema and loader capabilities. They are not universally required for every field; the invariant is that present-null must be representable without destroying presence information. Sqlx tags shown above are supplied by dbunit. Structology's generic defaults must not impose SQL semantics.

| Map input | Value slot | Has flag | Reverse Store |
| --- | --- | --- | --- |
| key absent | zero/nil | false | omit key |
| `"id": nil` | nil | true | `"id": nil` |
| `"id": 0` | pointer/value representing 0 | true | `"id": 0` |
| `"active": false` | false | true | `"active": false` |
| `"profile": nil` | nil profile | true | explicit null profile |
| `"profile": {}` | allocated profile, child flags false | true | empty object |
| `"items": []` | allocated empty slice | true | empty array |
| `"items": nil` | nil slice | true | explicit null array |

Allocate every marker holder, including nested ones. An absent nested parent must remain absent even if its child type has a default zero layout. A present empty parent needs an allocated nested value with all child flags false. Do not use `omitempty` as a substitute for presence: it conflates intentional zero/empty values with omission.

### 7.3 Compilation and map population

1. Accept a supplied Go type or explicit schema; otherwise infer a deterministic schema over all relevant maps and nested collection elements.
2. Prefer explicit schemas for empty/all-null fields and empty repeated values. Inference cannot discover an element type from no elements; return a clear schema-needed error or a documented generic mode, not an accidental string type.
3. Build nested element types bottom-up; generate the Has type and its parent row type.
4. Preserve exact external names separately from exported Go field names. Resolve collisions (`a-b`, `a_b`, `Has`, `1name`, Unicode, quoted SQL identifiers) deterministically; tags retain exact external keys.
5. Compile source-key lookup, converter, setter/getter, and marker setter per field, reusing StateType/Selector/Marker where appropriate.
6. Allocate a destination row, ensure the marker holder, look up each map key with `value, present := row[key]`, and assign only after successful conversion.
7. Recursively apply the same algorithm to nested maps and repeated objects. A single repeated field uses one merged element type, even when each element contains different keys.
8. Return typed batches and row provenance. Do not reuse mutable backing buffers across concurrent conversions.
9. Reverse Store recursively reads values and markers, emits only present fields, and distinguishes empty from nil collections.

Define scalar conversion behavior explicitly: exact integer range checks, signed/unsigned boundaries, json.Number, decimal preservation, floats including NaN policy, booleans, time, []byte, aliases, custom conversion hooks and supported interfaces. In strict mode, incompatible mixed types and null into non-nullable supplied fields fail with a full path. No silent `toolbox.AsInt` fallback to zero for malformed values.

### 7.4 Nested structures and BigQuery

Nested map keys are literal keys by default; `{"a.b": 1}` must not silently become `{"a":{"b":1}}`. Path-based mapping is an explicit option.

Required shapes include nested records, arrays of scalars, arrays of records, records containing arrays, sparse array elements, empty objects, and explicit-null parents. General structology capabilities may exceed any particular database's accepted shapes; database-specific rejection belongs in dbunit's schema adapter.

Guard recursive inputs: detect cyclic map/slice/struct pointers, impose configurable depth limits, and reject or explicitly represent cycles. Preserve order of arrays. Do not share nested marker holders between rows/elements.

Dbunit maps BigQuery schema modes and nested fields to structology schema descriptors. BigQuery may normalize distinctions such as null versus empty repeated values in storage; fixtures preserve author intent, then the BigQuery adapter applies documented normalization or rejects unsupported inputs. Do not promise distinctions the backend cannot retain.

### 7.5 Cache and performance contract

Cache keys include ordered external names, Go types, complete tags, nested schema fingerprints, marker options and converter policy. Cache compiled plans and field accessors, not populated rows. Bound admission of distinct dynamic schemas: evicting a Go cache does not guarantee runtime-created reflect types are reclaimed. Favor schema-driven shapes and reuse projections.

Benchmark cold compilation separately from warm conversion; scalar-only and nested workloads; forward and reverse directions; 10/1,000/100,000 rows; allocations and peak memory. The hot path must avoid per-cell `FieldByName`, tag parsing and type reconstruction.

### 7.6 Dbunit integration boundary

Dbunit resolves SQL types and passes exact field types/tags into structology.Maps.Load. Structology never imports dbunit, database/sql or sqlx. Dbunit may consume adapter presence metadata/accessors or inspect markers via structology to select load projections.

Reverse Store is needed for capture, export, diagnostics, equivalence tests and presence-aware JSON loading. Keep generated database values in run-owned adapted rows; copy selected values into run state only after the appropriate successful transaction boundary.

## 8. Preparation operations and fast loading

### 8.1 Explicit write semantics

| Operation | Semantics |
| --- | --- |
| Insert | Insert supplied rows; existing duplicate keys are errors. Empty dataset is a no-op. |
| Replace | Delete all rows in the explicitly named target/scope, then insert supplied rows. Empty dataset empties that scope. |
| Upsert | Insert missing keys and update present non-key columns according to marker presence. Requires explicit or verified keys and a certified dialect path. |
| DeleteMatching | Delete rows selected by explicit keys or a bound predicate. Empty key set deletes nothing. |

An empty `{}` in modern fixtures is data, not a command to delete a table. Default-only rows require an explicit supported mode. Legacy empty-record reset behavior is translated only by the compatibility layer, with an auditable diagnostic.

Replace defaults to transactional DELETE where supported. TRUNCATE is opt-in and capability-checked because it can implicitly commit, reset identities differently or interact differently with foreign keys. Do not disable foreign keys globally as an automatic convenience.

### 8.2 Strategy selection

Expose `Auto`, `Batch`, and `BulkRequired`. Auto chooses the fastest **certified semantically equivalent** path for the dialect, schema, operation, transaction mode and input presence shape. Report the selected strategy and reason. A bulk-unavailable choice falls back before writes; a failed bulk operation is not automatically retried with inserts.

- Batch uses SQLX `io/insert` with a measured batch-size default and dialect parameter limits.
- Native bulk uses SQLX `io/load` with explicit format and options; no direct COPY/LOAD DATA implementation in dbunit.
- Upsert is a separate semantic capability. SQLX's `WithUpsert` option existing does not prove every loader implements equivalent behavior.
- Generated-ID capture, returning columns, sparse defaults, custom encodings and transaction support can force Batch or a dedicated strategy.
- Empty batches are handled by dbunit before invoking loaders which access element zero.

Batch sizes are limited by maximum bind count, row width and memory. Benchmark small fixtures: metadata/type compilation overhead can outweigh bulk benefits. Auto thresholds should be based on measured behavior and remain configurable.

### 8.3 Presence-aware column projections

Has markers express intent; SQLX bulk loaders commonly derive one column list from the struct type. A field with Has=false can still be sent unless it is omitted from the physical projection.

Dbunit therefore compiles a write projection for each effective presence mask:

1. Determine present writable columns from Has, excluding generated/read-only fields unless explicitly supported.
2. Use a projected dynamic struct containing exactly that column set, or a certified SQLX column-restriction path.
3. Preserve explicit null columns in the projection.
4. Leave absent columns out so database defaults apply.
5. Pass projected typed batches to SQLX.

Preserve original row order by batching contiguous equal-mask runs by default. Combining nonadjacent masks can change generated IDs, trigger effects or upsert ordering; permit regrouping only with an explicit order-independent policy. Set limits on distinct projections and report degraded batching instead of constructing unlimited runtime types.

For explicit null identity values, honor dialect semantics or return an error. The inspected SQLX identity sequencer can treat nil pointers as requiring generation even when marked present; do not silently reinterpret explicit null as missing. Add integration tests and adjust SQLX or the strategy choice where necessary.

### 8.4 BigQuery JSON serialization bridge

The inspected SQLX JSON reader uses standard encoding/json and cannot infer omission from Has. Simply adding `json:"-"` to Has prevents marker leakage but does not omit absent payload fields. Adding `omitempty` loses present zeros and empty collections.

Preferred integration: enhance SQLX's JSON reader with an explicit presence-aware encoder option backed by structology's existing encoding support, including nested markers. This is SQLX work, not generic Maps adapter work.

Safe initial alternative: call `Maps.Store` on typed rows, then supply the resulting presence-filtered maps to SQLX's JSON load reader. The database write still goes through SQLX. Measure the extra allocation cost and make it visible in benchmarks. This bridge must retain numeric precision and the backend's required encodings.

Use JSON as the initial BigQuery nested loading format. Do not route RECORD/repeated-record fixtures through CSV. Gate Parquet support on schema/nullability/presence integration tests. Fix or isolate the inspected loader's package-level mutable CSV configuration before allowing concurrent custom CSV configurations.

### 8.5 Metadata and identifiers

Use SQLX metadata where available, with dbunit's normalized descriptors and explicit overrides when a driver reports incomplete metadata. Cache by database identity, catalog/schema, table, dialect and schema-generation/version key. Invalidate after schema changes; never reuse metadata across different supplied DBs just because table names match.

Represent identifiers as structured components with explicit quoting. Distinguish a quoted identifier containing a dot from a multi-part identifier. Preserve column case. Bind values using dialect placeholders. Never interpolate expanded data into SQL value positions. Raw authored SQL remains explicit user input; quoted identifier expansion must use a separate typed mechanism.

Nested metadata belongs in descriptors recursively. Avoid treating every unknown database type as TEXT. Unsupported conversions fail preflight with an override suggestion.

## 9. Transactions, isolation and execution order

### 9.1 Modes

| Mode | Ownership and visibility |
| --- | --- |
| PrepareAtomic (default for certified transactional DBs) | Dbunit owns one preparation transaction, commits before Act. Application can use its existing DB handle. |
| CallerTransaction | Caller supplies matching DB and Tx. Dbunit uses the Tx throughout requested operations and never commits or rolls it back. Caller must roll back after failure. |
| CaseRollback | Dbunit owns a transaction for prepare/action/expect and rolls back at the end. Act must use Run.Executor, not an unrelated DB connection. |
| ExplicitNonAtomic | For certified nontransactional paths such as BigQuery load jobs. Report completed/failed operations and cleanup policy; no atomicity claim. |

Default atomic requests fail early on a path that cannot provide atomicity. BigQuery examples must explicitly select non-atomic preparation or a tested staging-and-publish strategy. Multi-database cases have per-database boundaries, not distributed transactions; require an explicit partial-success policy before writing more than one database.

A load strategy must demonstrate that it respects caller transaction ownership. Passing `WithTransaction` is insufficient evidence. Include a rollback-after-load integration test and transaction visibility checks using an independent connection.

### 9.2 Execution sequence

1. Validate Case shape, sources, capabilities and database bindings.
2. Resolve/decode all reusable fixture content and parse all declared statements.
3. Compile dependency graph, mappings and static schema information. Fail static errors before writes.
4. Execute explicit schema setup. Some metadata cannot exist before DDL; mark that compilation stage deferred and resolve it afterward. Schema DDL may have independent commit behavior.
5. Expand run-state-dependent preparation inputs just before their operation. Expansion depending on database captures cannot all run at compile time.
6. Begin the owned preparation transaction; stop immediately if BeginTx fails.
7. Apply deletes in reverse dependency order, then load parent-before-child. Stable declaration order breaks ties.
8. Execute captures and explicit sequence adjustments at declared dependency points.
9. On the first write error, stop; roll back owned transactions and preserve the original cause plus rollback error. Never overwrite it with a later success.
10. Commit preparation. Publish transactional captures only after success; preserve “commit outcome unknown” when the driver cannot establish the outcome.
11. Run Act, then expand expectations using updated run state.
12. Read actual rows and aggregate all independent assertion failures deterministically.
13. Run requested cleanup in a bounded fresh context when the main context is canceled. Preserve both primary and cleanup failures.

Preparation is serial within a transaction initially. Add parallelism only between explicitly independent databases/schemas or independent owned transactions, with separate state and clearly reduced atomicity. A thread count must never mean goroutines sharing an error variable, mutable context and one transaction.

Cycle detection reports the dependency chain. Deferrable constraints may be enabled explicitly on a pinned transaction/connection if the dialect supports them. Any session setting change must be restored on that same session, with restoration errors reported.

Avoid pool starvation: plan metadata before borrowing the only connection where possible; metadata inside a caller transaction must use a transaction-capable path or explicit descriptors. Test MaxOpenConns(1), temp tables and uncommitted DDL. No hidden DB query may block waiting for a connection held by the current operation.

## 10. Toolbox expansion and macros

Retain `data.Map.Expand`, `ExpandAsText`, nested path lookup, arithmetic, collections, spreads and UDFs. Do not substitute a simpler string template engine.

Pipeline: decode with presence -> copy into run-owned state -> toolbox typed expansion -> explicit macros -> SQL-type conversion -> structology map adaptation -> database write. For expectations, defer predicates until actual-value comparison. Assertly macros that return predicates must not be eagerly converted into SQL column values.

Rules:

- Exact expressions can produce typed values, maps or arrays. Embedded text expressions produce text according to toolbox behavior.
- Source templates and engine defaults are immutable. Each run receives a deep copy of supported container values; functions are shared as immutable registrations and must obey a documented concurrency contract.
- State precedence: engine defaults < case state < run overrides < explicit captures, with reserved namespaces protected.
- Use one injected clock and optional deterministic random source per run. Time-sensitive expected values can refer to a captured instant.
- Preserve toolbox's compatibility behavior as an option. New fixtures should support strict unresolved-expression checking, implemented with expression-aware tracking rather than rejecting every dollar sign. Literal currency, PostgreSQL dollar quotes and positional placeholders must remain valid.
- UDF errors currently may leave expressions unchanged. Implement diagnostics around registered UDFs and, if necessary, add a backward-compatible error-reporting hook upstream. Do not claim strict evaluation is complete until UDF errors and unresolved paths are observable.
- Evaluate each authored expression once per declared phase. Do not repeatedly expand to a fixed point unless explicitly requested with cycle/depth limits.
- Map traversal order must not determine sequence values or side effects. Declare stateful captures as ordered steps; require pure UDFs for unordered field expansion or define a stable field order.

Construct a private toolbox ValueProviderRegistry and assertly context per engine/run. Copy known immutable provider registrations explicitly and override only in the private registry. Do not modify `assertly.ValueProviderRegistry` or import dsunit for its init side effects.

Provide parameterized SQL macros and explicit Query/Capture steps using the active executor. Validate argument counts and scalar cardinality: zero rows, one row, and multiple rows have documented behavior. Query values use bound arguments, never SQL string substitution.

Inventory compatibility macros: `sql`, `seq`, `pos`, nil, empty, cast, env, dates, time differences and assertly predicate providers. Legacy seq/pos behavior must be characterized from fixtures and given an explicit compatibility implementation; do not approximate it with MAX(id)+1 and call it concurrency safe.

Sequence operations distinguish deterministic local fixture counters, database sequences/identity state, and SQLX scoped sequence reservations. Each has different locking, rollback and advancement behavior. Explicit fixture IDs are the preferred portable choice. Generated key capture must use returning/backfill or a verified natural-key lookup, never assume contiguous last-insert IDs for a bulk load.

## 11. Validation contract

### 11.1 Scope and row matching are separate

- **Full** reads all rows in an explicitly specified table or bounded query scope and checks exact row cardinality.
- **Keys** selects rows for the concrete expected key tuples and checks every expected tuple. Rows outside the selected key set are ignored.
- **Query** executes explicit SQL and compares its result, with exact or contains policy stated independently.

An empty Full expectation asserts zero rows. An empty Keys expectation selects nothing and is rejected by default as vacuous unless `AllowEmpty` is explicit; it must never turn into a full-table read accidentally.

Keys must be concrete after expansion; a predicate is not a lookup key. Composite keys use typed tuple encodings with lengths/type tags, not string concatenation. Duplicate expected keys are errors in unique-key mode; duplicate actual keys are assertion failures. If duplicates are intentional, use multiset mode.

Unordered comparison requires keys or multiset matching. Ordered comparison requires an explicit query ORDER BY or a declared order column list compiled into SQL. Do not rely on database natural row order. For multiset predicate matching, use a correct one-to-one matching algorithm with bounded cost; reject ambiguous/oversized combinations requiring explicit keys rather than using greedy matching that can produce false failures.

Build key queries with dialect bind limits and composite-key semantics. Null-key behavior is explicit; never emit `col = NULL`. Accumulate actual row counts across batches and detect duplicates spanning batches.

### 11.2 Field and value semantics

Default field policy is expected-fields-only: every present expected field is checked; omitted fields are ignored. Full row cardinality does not imply every database column must appear in the expected fixture. Offer strict-fields mode separately.

Preserve distinctions between missing, null, zero, false, empty string, empty object and empty collection through normalization and comparison. Backend-specific normalization, such as BigQuery repeated-field behavior, is opt-in or attached to a documented dialect policy.

Normalization rules include:

- Exact integer/decimal comparison without float64 round trips.
- Explicit approximate float tolerances; no global coercion of numeric text to numbers.
- Distinguish text and binary []byte based on SQL metadata.
- Time comparisons with explicit timezone, layout and precision policy.
- JSON columns: recursive semantic comparison when selected; plain string comparison otherwise.
- Nested records and repeated records: recursive key/order/contains policies scoped by path, e.g. `items` keyed by `sku` while `events` preserves order.
- Predicate invocation through a private assertly context; escape syntax for literal macro-looking strings.

Dbunit enforces row cardinality and matching before/around assertly value comparison, so permissive assertion directives cannot accidentally weaken Full scope. Preserve supported assertly directives through explicit options or compatibility translation. Unknown directives fail rather than becoming database columns.

### 11.3 Reading and reports

Use context-aware database/sql querying with metadata-assisted scanning. Copy driver-owned bytes before advancing rows. Always close rows and check `Rows.Err()`. Query aliases must be unambiguous; duplicate result-column names are errors unless the user supplies an explicit positional mapping.

Reports contain case/step/dataset, source, table/query identity, row key, nested path, expected/actual value summary, mismatch category, counts, strategy, duration and transaction outcome. Truncation limits affect displayed examples, not pass/fail or total failure counts. Do not store entire huge actual result sets by default.

Failure aggregation is monotonic: one failed expectation makes the overall run fail regardless of later passes. Represent statuses with typed enums if useful, but derive success from errors and aggregate counts consistently.

For large comparisons, prefer streaming ordered keys or bounded partitioning. The initial in-memory comparator must enforce a documented row/byte bound. A future spill-to-disk comparator is optional and must not silently change comparison semantics.

## 12. Additional SQL parity features

### Mapping graphs

Support named logical fixtures mapped to multiple physical tables. Explicit mappings define source paths, destination columns, required fields, defaults, keys and associations. Compile a deterministic dependency graph and reject cycles unless a supported deferred-constraint path exists. Use typed composite keys for deduplication. Conflicting values for the same mapped row are errors by default; do not silently accept whichever map iteration happened first.

### Fixture export

Execute an explicit bound query, normalize values, then apply an ordered transformation list: ignore paths, overrides, replacements, timezone/layout handling, relative-date expressions, ASCII transformation and optional obfuscation. Export JSON/NDJSON first; CSV needs an explicit null/escaping policy. Preserve exact numbers and nested shape.

Obfuscation must have injectable deterministic randomness for reproducible fixtures and documented transformation order. It is not automatically a privacy guarantee. Writable destinations are explicit and separate from read-only embedded sources. A Replace instruction belongs in the manifest, not an empty first row.

### Schema dump and comparison

Normalize tables, columns, nullability, keys, indexes and defaults from SQLX metadata. Expose partial-metadata warnings/errors clearly. Compare type families using dialect rules and user overrides. Cross-vendor DDL conversion requires explicit supported mappings; reject unknown types/default expressions rather than generating misleading SQL.

### Cross-database data comparison

Bind each side to its own *sql.DB and query/args, then reuse normalization and row matching. Specify consistency boundaries; two independent database queries do not constitute a distributed snapshot. Support ignored paths and discrepancy reporting limits without treating omitted diagnostics as matching rows.

### Readiness and optional transport

Ping uses context deadlines, bounded retry policy and driver errors. Avoid string-based sleep loops.

If HTTP parity is required, implement a separate adapter module/package using the same serializable contracts, with httptest route coverage for every operation. No remote arbitrary Go callbacks. Configure listeners and authentication externally. Keep transport out of the core dependency path; return server lifecycle objects and errors rather than log.Fatal or global DefaultServeMux mutations.

## 13. Package layout

```text
dbunit/
  engine.go             # immutable configuration, DB bindings, options
  case.go               # explicit case/request contracts
  plan.go               # compilation and immutable steps
  run.go                # per-run lifecycle and state
  prepare.go            # write orchestration
  expect.go             # read/validate orchestration
  query.go              # explicit query and capture
  report.go             # outcomes, statistics and mismatch reporting
  errors.go             # typed errors and errors.Is/As support
  fixture/              # source contracts, FS/embed/AFS, decoders, manifests
  dbtest/               # testing.TB conveniences
  compat/dsunit/        # fixture translation, no import of legacy dsunit
  internal/schema/      # SQL metadata normalization and identifier handling
  internal/expand/      # toolbox state, private macro providers
  internal/load/        # SQLX strategy adapter and presence projections
  internal/match/       # keys, ordered/multiset matching, normalization
  internal/dialect/     # capability descriptors and SQL policy
  internal/mapping/     # logical-to-physical dataset mapping
  export/               # export, schema dump, schema/data comparison as needed
  examples/             # explicit files, embedded fixtures, nested BigQuery
  integration/          # isolated per-dialect tests, optional modules if needed
  testdata/             # manifests and fixtures referenced explicitly
```

Start with the smallest public surface; keep internal contracts internal until real extension needs exist. Structology's Maps implementation belongs entirely in its repository. Any SQLX serializer/loader corrections land in SQLX and are pinned as prerequisites.

Avoid importing all SQLX vendor packages in dbunit core. Consumers select driver and loader registrations explicitly. Audit `go list -deps` separately from `go mod graph`: a transitive module requirement does not by itself mean its packages are linked into dbunit. Prevent dsc and dsunit from entering the core package dependency graph.

## 14. Dialect certification matrix

A capability record is keyed by database product, driver and relevant version/features. It records transaction enlistment, load format, identifier fidelity, column omission/defaults, explicit null, generated keys, nested data, parameter limits and supported operations.

| Dialect | Initial loading route | Required certification |
| --- | --- | --- |
| SQLite | SQLX batch insert | Atomic replace; zero/null/default/presence; composite keys; rollback; pool size 1. |
| PostgreSQL | SQLX batch + native COPY | Caller Tx; schema-qualified and quoted mixed-case identifiers; nulls; defaults; exact numerics; cancellation. |
| MySQL | SQLX batch + LOAD DATA | InnoDB and nontransactional behavior distinction; local_infile prerequisite; warnings/coercion; encoding; explicit nulls; rollback. |
| BigQuery | SQLX JSON load | Nested RECORD/repeated schema; presence-aware JSON; large integers/numerics; completed job visibility; non-atomic reporting and cleanup. |
| SQL Server | SQLX batch + bulk after review | Error paths, statement lifecycle, caller Tx, identity settings and cancellation. |
| Vertica | SQLX batch/native after review | Transaction ownership, format fidelity and error reporting. |
| Oracle | SQLX-supported batch path after review | Driver integration, bind limits, sequences, date/number semantics; native load only if implemented/certified. |

Release baseline should certify SQLite, PostgreSQL and MySQL for ordinary SQL fixtures and BigQuery for nested use cases. Do not mark BigQuery done based only on map round-trip tests. Gate live tests with explicit environment variables and isolated namespaces; report skipped vendor coverage in CI artifacts.

## 15. Test strategy and regression gates

### Generic adapter tests (structology task)

- Dynamic and supplied types; both Load and Store; deterministic schemas and cached plan reuse.
- Missing/null/zero/false/empty strings, byte slices, empty object and nil/empty slices.
- Nested marker allocation, sparse repeated records and order preservation.
- Literal dotted keys, invalid/exported-name collisions and a payload column named Has.
- All-null/empty inference with explicit schema, incompatible types and conversion overflow.
- No input mutation; destination unchanged on failure; concurrent use of one compiled plan.
- Cycles/depth limits and property/fuzz tests for supported round trips.

### Dbunit unit and integration tests

1. Explicit paths work from a different cwd and with arbitrarily renamed Go test functions.
2. Two embed roots with identical logical paths stay isolated; missing embed assets never fall back to disk.
3. Fixture decode errors include location; CSV multiline and null semantics; NDJSON trailing errors.
4. Preparation rolls back when any dataset fails, including when a later dataset would succeed.
5. Begin failure causes zero writes; commit/rollback/cleanup failures preserve causal errors.
6. One failing expectation followed by passing expectations still returns AssertionError.
7. Replace empty dataset empties only its declared target; Insert empty dataset is no-op.
8. Has=false invokes defaults through omission; present nil writes NULL or a documented backend error; explicit zero stays zero.
9. Mixed presence masks preserve row order and generated-key correspondence.
10. Bulk vs Batch produce equivalent stored results for supported inputs, including nested BigQuery JSON.
11. Native load rollback/caller-transaction tests prove ownership; unsupported atomicity fails before mutation.
12. SQLX registration/driver absence is actionable; no runtime fallback after a failed write.
13. Full vs Keys vs Query, duplicate keys, composite keys, exact counts, multiset duplicates and ordering.
14. Nested path predicates, exact integers above 2^53, decimals, timestamp precision, null/empty distinctions.
15. Macro scopes, UDF errors, unresolved expressions, captured IDs, clock injection and parallel run isolation.
16. Mapping deduplication, conflict detection, dependency cycles and deterministic table order.
17. Context cancellation during reading/loading, rows.Err, driver errors and resource cleanup.
18. MaxOpenConns(1), temp tables, uncommitted schema visibility and metadata cache invalidation.
19. Export round-trip, schema/data comparison, and every optional HTTP operation's route.
20. No required external services for unit tests; live integration tests explicitly selected.

Run `go test`, `go test -race`, focused fuzzing and required live dialect suites at relevant milestones. Use a correctly versioned workspace for local integration, then test `GOWORK=off` against the pinned module graph. The earlier dsunit root test run was blocked by local dependency setup; do not use an unverified legacy test pass as a release baseline.

### Performance measurements

Separate stages: resolve/decode, expand, Maps compile, Maps.Load warm conversion, projection, SQLX service preparation, load, read and validation. Report rows/sec, bytes/sec, allocations, peak memory and number of DB round trips. Compare batch/native strategies and dsunit only on equivalent fixture semantics and isolated schemas.

Benchmark 10/1K/100K rows, narrow/wide tables, nested BigQuery records, single/mixed presence shapes, null-heavy data and cold/warm metadata caches. Include a compiled typed-slice SQLX baseline to measure dbunit overhead. Set numerical regression thresholds after collecting a reproducible baseline; do not promise an unsupported speedup factor.

## 16. Implementation phases and acceptance criteria

### Phase 0: contracts and dependency feasibility

- Create dbunit module with pinned dependencies and chosen Go minimum.
- Establish capability descriptors and explicit source/operation/matching contracts.
- Run minimal SQLX proofs for dynamic struct insertion, caller Tx and native-load formats.
- Confirm SQLX product imports, structology marker/tag compatibility and AFS embed isolation.
- Document nested BigQuery serialization and quoted-identifier gaps as tracked prerequisites.

Acceptance: small compilable probes establish actual dependency behavior; unsupported semantics have explicit errors/strategy restrictions. No global registries or cwd assumptions enter the API.

### Phase 1: structology Maps prerequisite (delegated)

- Implement Maps facade and reusable compiled adapters.
- Implement dynamic types, Has markers, explicit schema, bidirectional scalar and nested mapping.
- Add tests, docs and benchmarks; publish/pin the compatible revision when ready.

Acceptance: missing/null/zero and nested empty/null/absent states survive supported round trips; output integrates with SQLX Values/mapper behavior. Dbunit does not maintain a duplicate map adapter.

### Phase 2: fixture and expansion foundation

- Explicit Source/Resolver implementations, AFS embed and general FS.
- JSON/NDJSON/CSV/TSV decoding and strict versioned manifests.
- Run-owned toolbox state, private macros and explicit unresolved-expression policy.
- Compile-time source validation with provenance and immutable template caches.

Acceptance: embedded and file fixtures behave identically from any cwd; concurrent roots remain isolated; nested types and numeric precision survive expansion.

### Phase 3: correct preparation baseline

- SQLX batch insertion through Maps.Load.
- Explicit schema asset application via ApplySchema; reuse SQLX schema/metadata primitives (section 21).
- Insert, Replace and DeleteMatching with explicit transactions and dependency order.
- Presence projections, identity handling and query/capture steps.
- SQLite coverage plus PostgreSQL/MySQL batch integration.

Acceptance: all write-failure and presence regressions pass; source maps are immutable; default atomic preparation is demonstrably atomic on certified dialects.

### Phase 4: validation and first useful release

- Full/Keys/Query scopes, matching, normalization and structured errors/reports.
- Private assertly integration, nested expectations and testing.TB helpers.
- Case runner plus standalone Prepare/Expect examples.
- Same-dialect ExtractSchema and schema round-trip verification for the initial certified dialects.
- Initial Endly dbunit service, session/state/assertion adapters and MCP registration (section 20).

Acceptance: complete fixture -> application -> assertion examples pass; mismatches always return errors; failure aggregation and row cardinality cannot be weakened by later successes.

### Phase 5: native bulk and BigQuery nested data

- SQLX native strategies with certification gates and benchmarked selection.
- PostgreSQL identifier corrections where needed; MySQL coercion/warning checks.
- SQLX presence-aware JSON serializer or explicit Maps.Store bridge.
- BigQuery schema mapping, nested load/read/compare, job completion and non-atomic outcomes.

Acceptance: native and batch paths are semantically equivalent where advertised; BigQuery nested integration passes against a real isolated dataset; benchmarks quantify conversion and serialization overhead.

### Phase 6: full SQL utility parity and migration

- Upsert, table mappings, sequence policies, export/data export, advanced schema comparison/cross-vendor rendering and cross-DB data comparison. Basic same-dialect schema extraction is already required in Phase 4.
- Fixture compatibility translator and capability inventory.
- Port representative real dsunit SQL suites, including macro-heavy and nested cases.
- Optional remote adapter only if required by migrating consumers.

Acceptance: every SQL parity row in section 3 is implemented, explicitly deferred with a tracked consumer impact, or intentionally removed with a documented migration. A “full replacement” release requires closing required deferrals.

### Phase 7: release and hardening

- Race/cancellation/resource checks, dependency audit, public examples and migration guide.
- Published dependency pins; clean module build outside developer go.work.
- Document supported dialect/driver combinations and performance results.
- Verify no dsc/dsunit dependency, runtime test-name introspection or global mutable per-run state.

Acceptance: release checklist is reproducible in CI; all required dialect suites and API examples pass.

## 17. Migration plan

1. Inventory dsunit consumers by operation, dialect, source convention, macros/directives, mappings and remote use.
2. Convert discovery into explicit manifests or explicit source paths. Use existing files unchanged where possible.
3. Replace dsc registration with caller-owned `*sql.DB` plus explicit dialect and loader imports.
4. Translate empty leading record -> Replace, `@indexBy@` -> matching keys, `@fromQuery@` -> QuerySpec, and `@autoincrement@` -> explicit identity configuration. Explain any behavior that cannot be preserved exactly.
5. Retain `$...` toolbox expansion and supported `<ds:...>` predicate syntax through private evaluators. Do not silently substitute different seq/pos semantics.
6. Run dsunit and dbunit in separate isolated databases with the same inputs. Compare resulting data and assertion decisions; include deliberate failing cases so false-success behavior is detected.
7. Adopt embed fixtures by declaring exact roots and manifests in test code. No requirement to rename directories after test functions.
8. Enable Auto/native loading after correctness parity for the workload is proven.
9. Retire compatibility translation gradually; keep ordinary fixture data portable.

Compatibility code must not import dsunit or dsc, because doing so restores the dependency and init-side effects this design removes.

## 18. Risks and decisions to settle during implementation

| Risk / open detail | Proposed resolution and gate |
| --- | --- |
| Dynamic Go types retain runtime metadata | Limit schema diversity, compile once, reuse explicit schemas; cap presence projections. |
| Has marker does not automatically change bulk columns | Explicit SQL projection; sparse defaults tested across strategies. |
| Nested Has invisible to encoding/json | Presence-aware SQLX encoding or Maps.Store projection bridge. |
| Metadata incomplete or wrong for nested/custom types | Explicit schema overrides and integration-tested dialect adapters. |
| Native load escapes caller Tx | Capability gate and rollback/visibility integration tests; never silently downgrade atomicity. |
| BigQuery normalizes input distinctions | Preserve them in Maps; document/reject/normalize at the SQL adapter boundary. |
| Toolbox UDF failure visibility | Backward-compatible instrumentation/upstream hook before strict mode is declared complete. |
| Prefix/key-case differences between SQL and JSON | Keep external keys and SQL identifiers separate with exact mappings. |
| SQLX driver-specific assumptions | Certify product + driver combinations, not only product names. |
| Concurrent tests share one database | Require caller isolation or explicit coordination; engine thread safety does not isolate application data. |
| SQL dump/type translation loses semantics | Explicit mapping and unsupported-type errors; no universal cross-vendor claim. |
| Public API grows too large early | Keep generic extension points internal until fixtures and migration use cases justify exposure. |

Recommended defaults: explicit sources, strict decode, expected-fields comparison with exact Full row counts, serial preparation, atomic preparation on supported dialects, SQLX Auto strategy gated by semantics, no global FK disabling, no global macro mutations, and independent mutable state per run.

## 19. Definition of done

- [x] `plan.md` records the agreed architecture and prerequisite work.
- [ ] Structology Maps supports bidirectional dynamic/supplied struct conversion with recursive Has markers.
- [ ] Dbunit accepts *sql.DB and uses SQLX for fixture insertion/loading.
- [ ] Explicit file and embedded fixtures work independently of test names and cwd.
- [ ] Toolbox expansion and assertly predicate compatibility are documented and tested.
- [ ] All supported load strategies preserve omission/null/zero semantics.
- [ ] Nested BigQuery fixture preparation and validation have real integration coverage.
- [ ] Transaction ownership, failure aggregation and cleanup behavior pass regression tests.
- [ ] Full, Keys and Query comparisons have precise cardinality and matching guarantees.
- [ ] Required SQL utility parity is complete or explicitly tracked before claiming replacement readiness.
- [ ] Migration examples and standalone module CI are reproducible.
- [ ] Endly dbunit actions work through workflow, CLI and MCP with correct shared state and assertion accounting.
- [ ] Every certified e2e dialect supports explicit schema application and same-dialect extraction/restore with a completeness report.

## 20. Endly integration: required, with a small adapter

Added after inspecting `github.com/viant/endly` at `5119ce15`. This is a required integration target, not an optional HTTP service. The implementation plan includes source changes in Endly, while the core dbunit package remains usable independently.

### 20.1 What Endly actually depends on

| Inspected source | Current integration contract |
| --- | --- |
| `endly/service/testing/dsunit/service.go` | Registers 14 actions: register, create, script, sql, mapping, init, prepare, expect, query, freeze, dump, compare, checkSchema, sequence. Uses dsunit.Service and injects workflow state through SetContext. |
| `endly/service/testing/dsunit/contract.go` | Wraps dsunit request/response types. Expect, Query, Compare and CheckSchema responses expose `Assertion() []*assertly.Validation`. |
| `endly/service/testing/dsunit/init.go` | Registers the service and the global `AsTableRecords` UDF. |
| `endly/service/testing/dsunit/data.go`, `udf.go` | Generates fixtures, expands values, allocates sequence references, caches records, publishes records into workflow state, handles tags and output prefixes. |
| `endly/service/testing/dsunit/udf_map_test.go` | Requires both ordinary maps and data.Map inputs and useful errors for malformed table collections. |
| `endly/service/testing/dsunit/drivers.go` | Imports SQL drivers and bridges legacy sqlite3/oracle names through dsc and database/sql registration. |
| `endly/context.go` | `Background()` carries standard context cancellation; `Deffer` registers session cleanup. `AsRequest` expands request containers before constructing the typed request. |
| `endly/service/bootstrap/bootstrap.go` | Imports the dsunit service into the default runtime. |
| `endly/cli/runner.go` | Consumes the Assertion interface for test reporting. |
| `endly/service/manager/execution.go` | Counts assertion results from emitted events for stateful operations. |
| `endly/server/mcp/source_tools.go` | Native source tools are generated only for an explicit service list that currently includes dsunit. |
| `endly/server/mcp/source_tools_test.go` | Checks native action dispatch, shared session state and failed assertion accounting. |
| `endly/skills/embed.go`, `tools/generate_skills.py` | Ships embedded service skills and generates documentation from registered actions; generator has dsunit-specific function-skill handling. |

Observed workflows use file fixtures, inline `Data`, request assets, shared setup across multiple cases, SQL sequence results assigned through `post`, `$AsTableRecords(...)`, UUID expressions, and state references such as `dsunit.<key>`. The ETL example prepares databases, calls an application over HTTP, and then validates databases. Preparation must therefore commit before the external application call in the normal Endly path.

Do not copy all historical behavior automatically. In particular, deleting unresolved `$...` fields, mutation of input maps, global sequence state, and implicit empty-record deletion must not become undocumented dbunit defaults.

### 20.2 Balanced ownership and scope

Keep one engine and one set of core operation contracts. Endly owns only what cannot live in a standalone database testing library:

- Named connection registration, credential lookup and owned-pool cleanup.
- Conversion from serializable workflow requests to core dbunit requests.
- Workflow state input/output, action cancellation and fixture-root bindings.
- Assertion/report events, action discovery and MCP metadata.

Core dbunit owns preparation, validation, SQL execution, schema asset application/extraction and reports. It must not import Endly, implement a pipeline runner, open an HTTP listener or expose a second independent service implementation.

Create `endly/service/testing/dbunit`. Prefer composition of core DTOs in Endly requests; introduce extra fields only for Endly-specific connection/source selection. Avoid duplicating every field in a parallel contract. A small registration/request adapter is appropriate; a general service framework is unnecessary.

Initial Endly actions: **register, init, script, sql, prepare, expect, query, dump**. Schema loading and extraction are first-release requirements (section 21). Preserve readable action names and add advanced mapping, sequence, export, compare and checkSchema actions when their core operations are implemented and required by migration. The existing `create` action is the actual legacy route name even though some documentation calls it recreate; compatibility tests must use observed routes.

This staging does not remove the SQL parity requirements in section 3. It prevents optional utility work from blocking the first useful integration, while keeping schema round-trip support in that first release.

### 20.3 Connection lifecycle

`dbunit:register` accepts a connection name plus driver/DSN or a credential-backed connection specification. A Go embedding API can instead bind a borrowed `*sql.DB` by name. The connection registry belongs to the Endly session; there is no process-global database pool map.

The Endly adapter resolves credentials using the existing credential registry/secret lookup infrastructure, constructs the driver DSN through a small explicit adapter, opens/pings the pool with the operation context, then constructs the core Engine. Do not reuse dsc.Config or dsc connection factories. A compatibility decoder may translate legacy driver/descriptor/parameters fields without introducing a dsc dependency into the new service.

Register cleanup through `Context.Deffer`, using an idempotent close wrapper to account for cloned contexts and repeated close calls. Close only pools the adapter opened. Never serialize pools, transactions, credentials, engine objects or fixture filesystem instances into workflow state. A small internal session holder carries those values outside the public data.Map.

Re-registering a name with conflicting settings is an error by default. Explicit replacement must wait for outstanding operations and close only the previous owned pool. Session shutdown cancels operations and closes pools in a defined order.

Preserve required driver aliases explicitly at the adapter boundary. Test pure-Go SQLite support without CGO and avoid double-registering a driver name. Keep driver/loader imports in Endly's selected runtime registration packages; dbunit core must not import every vendor.

### 20.4 Workflow state and expansion

Pass `endlyCtx.Background()` into core operations. Endly's `(*Context).Background()` returns the operation context carrying cancellation and deadlines; do not substitute a fresh standard-library background context.

Each invocation snapshots the necessary workflow state into a run-owned data.Map and supplies the relevant UDFs. Merge only declared outputs/captures back into workflow state after successful execution. Do not call a mutable Engine.SetContext or share a run's map across concurrent actions.

There are two expansion boundaries today: Endly AsRequest expands requests, and dsunit later expands dataset content. Define ownership explicitly:

1. Endly expands action routing, connection names, source references and normal request parameters.
2. Raw fixture templates must be preserved until dbunit's execution phase. External fixture file contents naturally bypass request expansion.
3. For inline datasets, use a narrowly scoped request expansion policy so dataset payloads are deferred. Current AsRequest only special-cases workflow:run fields; OnRawRequest runs after expansion and cannot recover erased original expressions. Add an explicit per-route deferred-field mechanism or a narrowly tested dbunit policy at the AsRequest boundary.
4. An alternative `expandedData` field can explicitly designate already-expanded concrete rows; dbunit must not expand these again. Do not introduce heuristic “expand again until stable” behavior.
5. Ensure stateful UDFs, UUIDs and sequence allocations run once. Test direct Go service calls, workflow YAML and MCP requests against the same expansion contract.

New outputs live under an explicit user-selected capture path or `dbunit.<name>`. Legacy `dsunit` and `dsconfig` paths are available only in an explicit compatibility mode. Preparation captures publish after commit; a failed transaction must not leave IDs claiming successful insertion. External workflow calls occur between separate Prepare and Expect operations, not inside a long-lived database transaction.

### 20.5 Fixture-generation UDFs

`AsTableRecords` is more than a map conversion helper. It has database-fixture generation semantics: AutoGenerate, PostIncrement, tags, cached results, table sequence references, UUIDs, and state publication. **Do not move this behavior into structology.Maps.** Structology remains a pure representation adapter.

Provide an Endly `dbunit:hydrate` action or a namespaced `DbUnitRecords` UDF when this functionality is needed. Prefer one small hydration component that can be called by either surface, not two implementations. It should accept explicit table data and generation/capture specifications, return hydrated maps, and publish only documented outputs.

Do not register a competing global `AsTableRecords` while legacy dsunit is loaded: registration order must not change workflow semantics. Keep the old UDF on the old service during coexistence. If backward compatibility is later required, implement an explicit translator with parity tests covering existing data_test, udf_map_test and sequence workflows.

Characterize and document: caching scope, repeated calls, sequence reset, UUID evaluation, output prefixes, `!tag`, `${tag}`, slash-based references, parent references, AutoGenerate timing, and PostIncrement timing. Do not reproduce map iteration order or mutate caller data. Prefer an ordered explicit hydration specification for the new API.

### 20.6 Assertions, errors and operation accounting

Endly-facing results wrap core reports and implement `Assertion() []*assertly.Validation` where existing CLI/manager integration requires it. Return the core error as the action error even when a detailed assertion report is also available. An assertion mismatch must make the workflow/MCP operation fail and increment failed assertion counts.

Publish one authoritative assertion outcome per logical validation. The legacy expect route also publishes validator.AssertRequest events; blindly repeating that pattern can double-evaluate predicates or double-count results. Verify the event pipeline and use a report adapter that does not rerun the database query or predicate evaluation.

Assertions must remain available on a failed action, including when execution stops before later workflow steps. Test exact passed/failed counts through both CLI handling and service/manager event extraction. Formatting/truncation must never turn a failure into success or hide the total failure count.

### 20.7 Explicit assets, including embedded Endly tests

Expose an adapter API to bind a named fixture resolver to an Endly session, e.g. a Go embedding program associates `orders` with `fixture.Embed(&assets, "testdata/orders")`. YAML references the resolver by name; it cannot serialize an embed.FS value. Resolver names are session-scoped and absent registrations are errors.

For file-based workflows, callers configure an absolute fixture root or explicitly choose the workflow asset's base as the root once. Paths may be relative to that declared root. Never derive them from case names or runtime callers. Existing Endly `@request` syntax selects a request asset; distinguish it from a dbunit data source rather than interpreting every string starting with `@` as data.

Example target contract (proposed, compile/test against final DTOs):

```yaml
pipeline:
  connection:
    action: dbunit:register
    database: app
    driver: sqlite
    dsn: /tmp/dbunit-example/app.db
  schema:
    action: dbunit:script
    database: app
    fixtures: orders
    sources:
      - schema.sql
  prepare:
    action: dbunit:prepare
    database: app
    fixtures: orders
    datasets:
      - table: customers
        source: prepare/customers.json
        operation: replace
  # Existing Endly application/HTTP steps can run here.
  verify:
    action: dbunit:expect
    database: app
    fixtures: orders
    expectations:
      - table: customers
        source: expect/customers.json
        scope: full
  extract:
    action: dbunit:dump
    database: app
    tables: [customers]
    destination: /tmp/dbunit-example/schema.sql
```

The runner must create the isolated directory and bind `orders` before executing this example. Dump has a writable destination independent of the embedded source resolver. `dbunit:init` can combine registration and explicit schema application for convenience; it calls the same underlying operations and does not create another initialization engine.

### 20.8 Endly files and rollout

Add `service/testing/dbunit/{service.go,contract.go,session.go,state.go,init.go,README.md}` and focused tests. Split more files only as implementation size warrants. Add bootstrap registration alongside dsunit during migration. Retain legacy NoSQL workflows on dsunit; never transparently route them to SQL-only dbunit.

Update `server/mcp/source_tools.go`'s extendedServices list so routes generate `endly_dbunit_register`, `endly_dbunit_prepare`, `endly_dbunit_expect`, `endly_dbunit_query`, `endly_dbunit_script`, and `endly_dbunit_dump` as available. Test generated schemas from real RequestProvider values; generic interface fields alone will not describe the action contract usefully. Check action-policy enforcement, session sharing, cancellation, missing service behavior and operation failure reporting.

Add the embedded `endly-dbunit` skill/reference and update generation rules as needed. Keep existing dsunit skills during coexistence; document both names and the migration path accurately. Add examples to Endly's workflow/ETL and BigQuery fixtures when the corresponding core functionality is certified.

Initial acceptance suite:

- Register -> apply schema -> prepare -> query -> expect in one Endly session using SQLite.
- Extract schema -> apply to a second isolated DB -> prepare -> expect with equivalent behavior.
- Failed expectation reports failure and exact assertion counts via CLI and MCP.
- Inline nested maps preserve null/zero/empty/presence; file and embedded sources behave consistently.
- Stateful expansion happens exactly once and captures are visible in the next action.
- Independent sessions with the same DB/source names do not share state or resources.
- Cancellation, shutdown, register failure and borrowed DB ownership behave correctly.
- Legacy dsunit actions/UDFs continue working while new dbunit actions are registered.
- At least one real HTTP/ETL workflow runs with committed preparation and post-action expectations.
- BigQuery nested schema/load/read/expect workflow passes before claiming BigQuery Endly support.

Implement this adapter immediately after core Prepare/Expect and schema round-trip operations become usable. Do not defer Endly until every advanced export/mapping feature exists; use Endly integration tests to validate core contract design early.

## 21. Schema loading and extraction: initial e2e requirement

Schema application and extraction are required for both Go-based and Endly e2e tests. This section promotes same-dialect extraction from the later utility phase into the first useful release. Cross-vendor type conversion remains a later feature.

### 21.1 Reuse SQLX, with clear limits

Inspected `sqlx/io/schema/service.go` provides `Service.EnsureTable`; `Table.CreateSQL` in `table.go` renders create-if-absent DDL from mapped columns. This helper is intentionally small: it does not manage migrations, raw DDL, defaults, automatic IDs or destructive operations. It rejects automatic IDs and requires known nullability. It is useful for simple generated fixture tables, but cannot recreate arbitrary existing schemas faithfully.

SQLX metadata already exposes catalogs/schemas, tables/columns, primary/foreign keys, indexes and sequence metadata through `metadata.New().Info(...)` with the corresponding info.Kind and sink types. SQLX's dialect supports identifier and storage-type rendering. Reuse these APIs rather than adding a second product registry to dbunit.

The inspected code does not establish a complete general-purpose schema dump service. Add reusable extraction/rendering primitives to SQLX where missing, keeping its existing EnsureTable contract intact. Prefer small normalized schema descriptions plus dialect implementations over a new schema migration framework.

Ownership:

- SQLX: database introspection, dialect-specific DDL extraction/rendering and reusable schema descriptions.
- Dbunit: selected object scope, explicit source/destination assets, application order, errors/completeness reporting, and test setup orchestration.
- Endly: workflow action wrappers, credentials, state and artifact references.

### 21.2 Minimal core API

```go
// Proposed contracts, implemented once and used by both Go and Endly.
func (e *Engine) ApplySchema(ctx context.Context, req SchemaApplyRequest) (*SchemaResult, error)
func (e *Engine) ExtractSchema(ctx context.Context, req SchemaExtractRequest) (*SchemaArtifact, error)
```

Apply accepts an ordered list of explicit SQL sources or an extracted schema artifact; an optional simple table-spec source may use SQLX EnsureTable. Extract accepts an explicit database/schema/table selection and returns SQL/manifest content plus metadata and a completeness report. Returning content supports in-memory round-trip tests; writing to an explicit destination is a convenience wrapper.

Use `script` for schema file application in Endly and `dump` for extraction. `init` composes registration and application. Avoid adding several synonymous actions for the same behavior. SQL statements for ordinary data operations still use `sql`.

### 21.3 Apply semantics

- Explicit source order; local files, named AFS roots, embedded FS and inline statement slices all work.
- Default apply executes authored DDL and fails on database errors. It does not silently drop existing objects or infer a migration.
- `Ensure` mode is explicitly limited to compatible create-if-absent table specifications; a successful existence probe is not proof of schema equivalence. Verify structure when equivalence is requested.
- Reset/recreate is explicit, limited to the selected isolated target, and requires an admin binding where appropriate.
- Statement handling must support the certified dialect's quoting/comments and procedural blocks. Never `strings.Split(sql, ";")`. Prefer explicit statement arrays or a tested dialect-aware script reader; reject unsupported delimiter constructs with a source location.
- DDL transactions are capability-based. Report prior completed statements on nontransactional failure; do not promise rollback where DDL implicitly commits.
- Apply invalidates affected metadata/adapter caches and verifies required schema objects before fixture insertion.
- The operation context controls SQL calls; session-specific DDL runs on a pinned executor when required.

### 21.4 Extract semantics and completeness

Start with **same-dialect extraction of explicitly selected tables and their required schema objects**. No data rows are exported. Preserve native type details, nullability, default expressions, generated/identity definitions, ordered composite keys, foreign keys and indexes for certified cases.

Prefer faithful native definitions when available; use SQLX metadata for discovery, dependencies and validation. Some inspected MySQL code already queries SHOW CREATE TABLE for sequence purposes, but that is not yet a schema export API. Promote reusable behavior rather than importing a private sequence helper. SQLite stored schema definitions and PostgreSQL catalog reconstruction require dedicated product implementations/tests; do not assume every product has an equivalent SHOW CREATE operation.

Include an artifact manifest recording product/driver, source catalog/schema, selected objects, dependency order, supported features, omissions and checksum. This can accompany schema.sql without imposing a large new file format on users.

Completeness rules:

1. Define the requested scope: tables only, tables plus constraints/indexes, or additional named object classes.
2. Fail if a requested required feature cannot be exported accurately. AllowPartial must be explicit and emits a clear machine-readable omission list.
3. Resolve foreign keys to unselected tables through an explicit dependency policy: include dependencies or reject. Do not export an artifact that quietly references nonexistent targets.
4. Preserve views, triggers, routines, extensions, partitioning, collation, generated expressions and other vendor features only when supported and requested. Do not imply full-database backup capability from table-level export.
5. Identity definitions and current counter values are separate options. Default schema-only extraction preserves definitions; data-dependent next values belong to an explicit sequence-state option.
6. Target schema remapping operates on parsed structured identifiers, never arbitrary string replacement within native SQL.
7. Produce deterministic object ordering and stable rendering where possible, without rewriting expressions whose semantics are not understood.

### 21.5 BigQuery schema handling

Extract and restore nested RECORD/STRUCT fields, field modes (required/nullable/repeated), and table options needed by the selected workload. Preserve partitioning and clustering when requested/certified. A flat information_schema column list alone is insufficient for nested schema fidelity; use the SQLX metadata/product integration and extend it if the driver does not expose complete nested fields.

The reusable SQLX schema description must carry nested fields. Dbunit feeds that schema into structology MapField definitions for data loading. Reject unsupported table types/options rather than flattening nested records into strings. BigQuery schema application and load jobs use their documented nontransactional lifecycle with bounded completion waits.

### 21.6 Proof required before release

For each certified dialect: create representative source schema -> extract -> apply to a fresh isolated target -> compare normalized schema -> insert identical fixtures -> verify the same query results and constraint behavior.

Required cases: default values, omitted/null/zero columns, identity definitions, decimal precision, composite-key ordering, foreign keys, indexes, reserved/mixed-case identifiers, quoted names containing dots and selected-schema remapping. BigQuery adds nested/repeated fields and relevant table options. Round-trip tests must exercise defaults/constraints, not only compare DDL text.

Deliver schema apply early in Phase 3 and same-dialect extraction by Phase 4, alongside the initial Endly adapter. PostgreSQL/MySQL/BigQuery certification expands with Phase 5. Same-dialect extraction is a release gate for every advertised supported e2e dialect; unsupported dialects may not be labeled fully supported. Cross-vendor rendering, full database recreation and advanced schema diff remain later parity work.

## 22. Implementation status (2026-10-20)

This section records implementation progress; earlier design examples remain proposals where they differ from exported Go contracts.

Implemented in the local checkouts:

- `dbunit`: engine, explicit fixture resolvers (including AFS embed), strict JSON/YAML manifests, SQLX batch preparation, native-loader opt-in, dependency ordering, owned/caller/case-rollback transaction modes, query/expect/report APIs, Go test helper, export and cross-query comparison.
- `structology.Maps`: bidirectional dynamic structs with nested presence markers. SQL ownership remains outside structology.
- `sqlx/io/schema`: native schema artifacts, completeness/omission reporting, explicit scoped extraction, SQL script parsing and schema application. Dialect-specific support is described in package documentation; live database certification is separate from query-mock tests.
- Endly: separate `dbunit` service, session database ownership, explicit asset roots, deferred fixture/argument expansion, typed assertion errors, manager/CLI counts and MCP exposure. The legacy `dsunit` service remains available.
- Toolbox predefined UDFs are registered in a private per-run registry. Custom UDFs override defaults. Tests exercise `$AsInt`, `$AsBool`, `$AsFloat`, `$ToUpper`, nested structures, caller input preservation, query arguments and exactly-once invocation. SQL text itself is not expanded by the core query API.

Verification includes executable SQLite integration tests, race tests for dbunit and structology, SQLX schema round trips, and focused Endly service/manager/CLI/MCP workflows. The README lists runnable local commands and the supported API shape.

Release work still requires live database certification for advertised native loaders/dialects. Mocked metadata tests establish API and SQL behavior, not a live product compatibility guarantee. Advanced schema objects and legacy dsunit directives that are unsupported must continue to produce explicit errors/omissions rather than success claims.

### Final acceptance additions

- CSV scalar conversion is driven by SQL column metadata; numeric-looking text columns remain text. Timestamp/date and base64 binary fixture round trips are covered.
- PostgreSQL 16 and MySQL 8 live schema extraction/application tests passed in isolated temporary Docker databases, including counter-state replay and constraints. PostgreSQL FK cycles restore through a separate constraint phase; unsupported MySQL cycles are reported explicitly.
- Live SQLX native-loader tests passed for PostgreSQL/MySQL presence/default/null/zero/text fidelity, owned rollback and caller transactions. The MySQL adapter now rejects server warnings instead of reporting silent coercion as success.
- Core `Exec` owns explicit DML execution and argument expansion. Endly delegates to it; transaction and loader capabilities are explicit service options.
- A local one-iteration Apple M4 Max SQLite batch benchmark measured about 269k rows/s for a uniform 10,000-row batch and 208k rows/s for alternating sparse shapes. These are development baselines, not portable guarantees or a comparison against dsunit.
- BigQuery live certification and unsupported advanced schema/legacy directive coverage remain explicit limitations.

Endly verification note: focused dbunit, legacy dsunit, manager, CLI, MCP and request-expansion suites pass. Full root-package tests still report `TestNewDefaultState` and `TestAbstractService_GetHostAndSSHPort`; both failures were reproduced using the original HEAD module graph. These were not introduced or hidden by the dbunit changes. The pre-existing change to `service/system/process/service_test.go` was preserved.

## 23. Fixture hydration and mapping implementation

The hydration/mapping work is now represented by explicit APIs rather than implicit test discovery or the legacy global AsTableRecords UDF:

- `Engine.Sequence` resolves invocation-local counters from explicit starts, captured state paths, or exact-integer seed queries. Database seeds are snapshots, not ID reservations.
- `Engine.Hydrate` supports ordered before/per-row generation, UUIDs, absent-versus-null semantics, explicit tag fields, forward references, captures, and post-increment state.
- `CompileMapping` builds reusable table projections; `Engine.Map` adds explicit asset resolution and expression expansion. Nested/repeated data, parent/root paths, composite-key deduplication, literal defaults, required fields, dependency ordering and conflict errors are supported.
- `fixture.Resolved` and `Source.Expanded` preserve the expansion boundary through serialization, compilation, preparation and expectations.
- Endly routes delegate these operations and use explicit state selectors for composing generated sources and datasets. The existing AsTableRecords UDF remains unchanged.
- README, fixture documentation, contribution guidance, changelog and runnable examples accompany the API. LICENSE and NOTICE retain dsunit's original text.

Remaining restrictions: one mapping node per physical table; dependency/reference cycles fail; no implicit legacy slash/tag/caching compatibility; generated IDs are fixture plans until preparation succeeds. BigQuery live certification remains separate release work.

### Cross-database and parent/child acceptance

- Separate database seed queries produce independent counters; multiple children retain their selected parent's generated ID in either hydration table order.
- Explicit captured sequence state supports continuation between workflow actions without a global counter or cached table state.
- Endly's automatic action-result publication now stores completed response values without evaluating them again. The cross-database hydration/mapping/preparation workflow verifies three authored UDF calls and zero evaluations of the expression-like strings those calls return.
- Dbunit race tests, vet, and CGO-disabled tests pass. Focused Endly workflow, manager, legacy dsunit, CLI, and MCP suites pass with the publication fix.
