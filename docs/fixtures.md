# Fixture hydration, sequences, and mappings

Hydration generates concrete logical records. Mapping projects those records into physical tables. Preparation writes the resulting datasets. Each step is explicit and can be inspected independently.

```text
raw source -> Hydrate -> resolved source -> Map -> resolved datasets -> Prepare
```

The output sources carry `expanded: true`. Keep that marker when passing data between steps: it prevents strings returned by UDFs from being interpreted as templates a second time.

## Hydrate logical records

```go
hydrated, err := engine.Hydrate(ctx, dbunit.HydrateRequest{
    Sequences: []dbunit.FixtureSequence{{Name: "orderID", Start: 100}},
    Tables: []dbunit.HydrationTable{{
        Name: "orders",
        Source: fixture.Maps([]map[string]any{{
            "customer": map[string]any{"id": 7, "name": "Alice"},
            "items": []any{
                map[string]any{"sku": "A", "quantity": "$AsInt(2)"},
                map[string]any{"sku": "B", "quantity": 1},
            },
        }}),
        Generate: []dbunit.Generation{{Field: []string{"id"}, Sequence: "orderID"}},
        Captures: []dbunit.HydrationCapture{{Field: []string{"id"}, Path: "orderIDs", Append: true}},
    }},
})
if err != nil { return err }
```

The first order receives ID 100, and the sequence result exposes the next value. `Start` includes zero; omitted `Step` means one. Sequences are invocation-local: no process-wide cache or counter is modified. Reusing a request starts from the declared values again.

For continuation, put a prior sequence's `Next` into explicit request state and use `FixtureSequence.StartFrom`. For a database-derived starting value, use `Seed: &dbunit.QueryRequest{SQL: "SELECT COALESCE(MAX(id), 0) + 1 FROM orders"}`. A seed must return exactly one integer scalar. It is a snapshot for isolated test preparation, **not an ID reservation**; concurrent writers can invalidate it.

`engine.Sequence` resolves sequence specifications without generating rows. Hydration also returns final sequence states, so the caller can continue them explicitly.

Each `Seed.Database` selects an explicitly bound database, so parent and child counters can seed from separate handles. Seed queries run in declared sequence order. These independent reads do not form a shared snapshot across databases. A caller transaction applies only to its matching default database; use separate invocations when separate transactions are required.

### Ordered generation

- `Before` runs once per table entry and writes private state aliases. It also runs for empty sources.
- `Generate` runs in declared order for each row. A generation selects a sequence, UUID, or value expression.
- `Field` is an exact nested path such as `["metadata", "id"]`. `State` can make a generated value available to later rules/templates.
- Existing fields—including explicit null—are retained unless `Overwrite` is set. Retained fields do not consume sequence values.
- `UUID: true` uses a cryptographically random UUID v4. `HydrateRequest.UUID` provides a per-invocation Go test hook for deterministic examples.
- `HasValue` distinguishes an explicit null generation value from an omitted value.
- `PostIncrement` increments declared private state paths once after a nonempty table entry. It is independent of per-row sequence allocation.

Ordinary fixture values then expand through toolbox/data, including `$AsInt(...)`, nested arrays and custom functions. Generated values are not expanded again. Input sources and caller state remain unchanged.

## Tags and references

Use `TagField: []string{"!tag"}` to explicitly select a tag field. It is removed from emitted rows. Tag names are unique within a named hydration table. No tag-field convention is enabled implicitly.

A `RecordReference` names the destination `Field`, target hydration `Table`, target `Tag`, and target `From` path. An empty `From` selects the complete target row. References resolve after generation/expansion, so forward references are supported. Missing targets and reference cycles are errors. `Overwrite` controls replacement of existing destination values.

Tags and references use logical hydration table names, not SQL table-name parsing. Exact path slices preserve literal dots in map keys.

During row generation and expansion, private `row`, `table`, zero-based `index`, and `tag` values are available to toolbox expressions. For example, `Tag: "${row.parentTag}"` resolves each child’s own parent tag once; later parent rows do not change that reference. Reference values are assigned after ordinary expansion, so templates in the same pass cannot consume newly assigned reference fields.

Captures run after references resolve. `Append` collects values in source order; duplicate scalar capture destinations fail. Captures are returned as data and do not mutate the caller's state. `StateCaptures: []dbunit.HydrationStateCapture{{From: "counter", Path: "next.counter"}}` explicitly selects final private state values, including `PostIncrement` results. Capture destinations are literal dotted paths; overlapping destinations fail before generation. They describe planned fixture values until insertion succeeds.

## Map one logical fixture into related tables

Continuing the hydration example:

```go
mapped, err := engine.Map(ctx, dbunit.MapRequest{
    Source: hydrated.Tables[0].Source,
    Mapping: dbunit.Mapping{Name: "orders", Tables: []dbunit.MappingTable{
        {
            Name: "customers", Table: dbunit.Table("customers"),
            Path: []string{"customer"}, Keys: []string{"id"},
            Columns: []dbunit.MappingColumn{{Name: "id"}, {Name: "name"}},
        },
        {
            Name: "orders", Table: dbunit.Table("orders"),
            DependsOn: []string{"customers"}, Keys: []string{"id"},
            Columns: []dbunit.MappingColumn{
                {Name: "id"},
                {Name: "customer_id", Path: []string{"customer", "id"}},
            },
            Associations: []dbunit.MappingTable{{
                Name: "items", Table: dbunit.Table("order_items"),
                Path: []string{"items"}, Keys: []string{"order_id", "sku"},
                Columns: []dbunit.MappingColumn{
                    {Name: "order_id", Scope: dbunit.MappingParentRow, Path: []string{"id"}},
                    {Name: "sku"}, {Name: "quantity"},
                },
            }},
        },
    }},
})
if err != nil { return err }

prepared, err := engine.Prepare(ctx, dbunit.PrepareRequest{Datasets: mapped.Datasets})
```

Create the three destination tables before preparation. The mapping produces customers, orders, and order items in dependency order. Associations depend on their mapped parent; `DependsOn` declares additional relationships. Replacement deletes tables in reverse dependency order.

### Paths and scopes

A table `Path` selects a relative source object or repeated collection; an empty path selects the current object. Column paths are exact key segments; an omitted column path defaults to its destination column name.
Table paths traverse repeated collections in source order, including arrays at intermediate path segments. Missing/null paths and empty collections produce no rows. An empty dataset is still returned for each mapped table, so an explicit `Replace` can clear it.

| Scope | Column reads from |
|---|---|
| `MappingSource` | Current selected source object (default) |
| `MappingRoot` | Original logical input record |
| `MappingParent` | Immediate parent source object |
| `MappingParentRow` | Immediate parent's projected physical row |
| `MappingElement` | Current repeated scalar element; the path must be empty |

`Default` is a literal fallback for an absent value. `HasDefault` permits an explicit null fallback in Go; JSON/YAML decoding recognizes an explicit `default: null`. Defaults never silently replace a present null. `Required` rejects missing/null values by default; false, zero and an empty string satisfy it. `OnMissingRequired: MissingRequiredSkip` explicitly skips that mapped row and its descendants, including when a key is missing/null.

### Deduplication and conflicts

`Keys` declares a composite physical-row identity. No keys means no deduplication. Keys must be present, nonnull strings, numbers, booleans or times. Components have type tags and length boundaries, so strings and numbers remain distinct and composite values cannot collide through concatenation. Numeric Go widths and `json.Number` normalize by their exact numeric values.

For duplicate keys, complementary absent columns merge. Two different present values for the same column—including null versus a value—are an error. Nested column objects and arrays are compared as complete values; their missing subfields are not implicitly merged. The mapper does not pick an arbitrary winner or conflate absence with zero.

A mapping has one writer per physical table. Separate nodes targeting the same database/table are rejected; consolidate those projections into a single table mapping. Dependency cycles and unknown dependency names fail during compilation.

For repeated use, `CompileMapping` returns a reusable `MappingPlan`. Its `Map` method accepts concrete rows and performs no expression evaluation. `Engine.Map` adds explicit source resolution and toolbox expansion for raw input; sources already marked expanded bypass that step. Mapping performs no SQL writes.

## Errors and transaction boundaries

Hydration/mapping failures return errors before `Prepare` is called. Neither operation claims database-generated identities, reserves sequence ranges, or commits data. Author seed queries as read-only scalar SQL through the selected connection or supplied caller transaction; dbunit does not reserve or advance database sequence ranges.

`Prepare` retains its normal transaction semantics. If it fails, treat generated IDs and captures as uncommitted fixture plans. In Endly, hydration results can be explicitly captured and inspected before preparation; a prepare success capture is published only after successful preparation.

Legacy dsunit's implicit caching, slash references, and global `AsTableRecords` mutation are not enabled by these APIs. The explicit rules above are the migration contract.

## Cross-database counters and parent/child IDs

Every seed query identifies its database binding explicitly. Keep independent counters under independent names:

```go
sequences := []dbunit.FixtureSequence{
    {Name: "customerID", Seed: &dbunit.QueryRequest{
        Database: "crm", SQL: "SELECT COALESCE(MAX(id), 0) + 1 FROM customers",
    }},
    {Name: "orderID", Seed: &dbunit.QueryRequest{
        Database: "sales", SQL: "SELECT COALESCE(MAX(id), 0) + 1 FROM orders",
    }},
}
```

Bind both handles with `WithDatabase` in Go or register both names in the same Endly session. A sequence name is not inferred from a table, the active database, or map iteration order. Reusing a single explicitly named sequence intentionally shares its local counter across entries; distinct names remain independent.

For parent/child relationships, generate each parent's ID once, tag that parent, and reference it from each child. For example, parent records carry `"!tag": "alice"` / `"!tag": "bob"`; child records initially carry `"customer_id": "alice"` / `"customer_id": "bob"`. Configure the parents with `TagField: []string{"!tag"}` and the children with:

```go
References: []dbunit.RecordReference{{
    Field: []string{"customer_id"},
    Table: "customers", // logical hydration table name
    Tag: "$row.customer_id",
    From: []string{"id"},
    Overwrite: true,
}},
```

Each child receives its own selected parent's generated ID—even when parent IDs include zero, several parents have several children, or child records precede the parent table in the hydration request. Child primary keys come from their own sequence. This avoids relying on a mutable “last parent ID” alias.

For a nested logical object mapped to tables, `MappingParentRow` reads the immediate projected parent's ID directly. Use this when the relationship is already represented structurally in the source; use tag references for independent logical collections.

Preparation still uses one database per request. For cross-database data, issue separate preparation actions against the intended bindings. Seed queries and captures do not create a distributed transaction, and failure in a later database cannot roll back an earlier committed action. Within one database, normal dependency ordering and atomic preparation apply.
