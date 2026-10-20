# Contributing

Contributions to dbunit are welcome. Open an issue or pull request with a reproducible example, the database and driver versions, and the expected behavior. Remove credentials and private fixture data from examples.

## Development setup

Use the Go version declared in `go.mod` or newer. This development checkout uses sibling replacements for Viant dependencies; clone those repositories next to `dbunit` and check out versions containing the APIs used here. See the README for the current release status.

Run commands from the `dbunit` directory:

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
CGO_ENABLED=0 GOWORK=off go test ./...
```

The default suite uses an isolated in-memory SQLite database. PostgreSQL/MySQL native-load tests are opt-in through `DBUNIT_TEST_POSTGRES_DSN` and `DBUNIT_TEST_MYSQL_DSN`. Use dedicated test services: these tests create and remove uniquely named database objects. Never point them at production.

## Changes and tests

- Keep database-specific loading and reusable schema introspection in SQLX.
- Keep generic map/struct conversion in structology.
- Keep fixture paths explicit and preserve absent, null, and zero values.
- Test failure paths, transaction ownership, input immutability, and expression evaluation counts when changing preparation behavior.
- Include a small reproducible test for a bug fix and update affected examples and documentation.
- Run `gofmt` on changed Go files. Avoid unrelated formatting or dependency changes.

For performance work, include the command, Go/database versions, row count, field shape, and allocation results:

```sh
GOWORK=off go test -run '^$' -bench BenchmarkPrepareBatch -benchmem
```

Contributions are provided under the project's Apache License 2.0. Preserve existing attribution and notices.
