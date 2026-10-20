package dbunit

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/sqlx/metadata/database"
	"github.com/viant/sqlx/metadata/info"
	"github.com/viant/sqlx/metadata/registry"
	"github.com/viant/toolbox/data"
	"reflect"
	"strings"
)

type Engine struct {
	db        *sql.DB
	databases map[string]*sql.DB
	dialect   string
	fixtures  fixture.Resolver
	state     data.Map
	maxRows   int
	native    *NativeLoadOptions
}

// NativeLoadOptions is an explicit caller assertion that the registered SQLX
// loader has been certified for this driver and workload. Auto remains Batch
// unless this option is supplied.
type NativeLoadOptions struct {
	Transactional bool   `json:"transactional,omitempty"`
	Format        string `json:"format,omitempty"`
	MinRows       int    `json:"minRows,omitempty"`
}

func WithNativeLoad(capability NativeLoadOptions) Option {
	return func(e *Engine) error {
		if capability.MinRows <= 0 {
			capability.MinRows = 1000
		}
		e.native = &capability
		return nil
	}
}

type Option func(*Engine) error

func WithDialect(name string) Option {
	return func(e *Engine) error { e.dialect = strings.ToLower(name); return nil }
}
func WithFixtures(r fixture.Resolver) Option {
	return func(e *Engine) error { e.fixtures = r; return nil }
}
func WithDatabase(name string, db *sql.DB) Option {
	return func(e *Engine) error {
		if name == "" || db == nil {
			return fmt.Errorf("named database requires name and handle")
		}
		e.databases[name] = db
		return nil
	}
}
func WithState(state data.Map) Option {
	return func(e *Engine) error { e.state = cloneMap(state); return nil }
}
func WithMaxRows(n int) Option {
	return func(e *Engine) error {
		if n < 1 {
			return fmt.Errorf("max rows must be positive")
		}
		e.maxRows = n
		return nil
	}
}
func New(db *sql.DB, options ...Option) (*Engine, error) {
	if db == nil {
		return nil, fmt.Errorf("dbunit: nil database")
	}
	e := &Engine{db: db, databases: map[string]*sql.DB{}, maxRows: 10000}
	for _, o := range options {
		if err := o(e); err != nil {
			return nil, err
		}
	}
	if e.dialect == "" {
		name := fmt.Sprintf("%T", db.Driver())
		switch {
		case strings.Contains(strings.ToLower(name), "sqlite"):
			e.dialect = "sqlite"
		case strings.Contains(name, "pq."):
			e.dialect = "postgres"
		case strings.Contains(strings.ToLower(name), "mysql"):
			e.dialect = "mysql"
		default:
			return nil, fmt.Errorf("dbunit: driver %s requires WithDialect", name)
		}
	}
	return e, nil
}
func (e *Engine) database(name string) (*sql.DB, error) {
	if name == "" {
		return e.db, nil
	}
	db := e.databases[name]
	if db == nil {
		return nil, fmt.Errorf("unbound database %q", name)
	}
	return db, nil
}
func (e *Engine) quote(parts Identifier) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("table identifier is required")
	}
	q := `"`
	if e.dialect == "mysql" || e.dialect == "bigquery" {
		q = "`"
	}
	if e.dialect == "bigquery" {
		parts = Table(strings.Join(parts, "."))
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		if p == "" || strings.ContainsRune(p, 0) {
			return "", fmt.Errorf("invalid identifier component")
		}
		out[i] = q + strings.ReplaceAll(p, q, q+q) + q
	}
	return strings.Join(out, "."), nil
}
func (e *Engine) bind(n int) string {
	if e.dialect == "postgres" || e.dialect == "postgresql" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}
func (e *Engine) executor(name string, c runConfig) (Executor, *sql.DB, error) {
	db, err := e.database(name)
	if err != nil {
		return nil, nil, err
	}
	if c.tx != nil {
		if name != "" {
			return nil, nil, fmt.Errorf("caller transaction requires default database")
		}
		return c.tx, db, nil
	}
	return db, db, nil
}

type RunOption func(*runConfig) error
type runConfig struct {
	mode  TransactionMode
	tx    *sql.Tx
	state data.Map
}

func WithTransaction(tx *sql.Tx) RunOption {
	return func(c *runConfig) error {
		if tx == nil {
			return fmt.Errorf("nil caller transaction")
		}
		c.mode = CallerTransaction
		c.tx = tx
		return nil
	}
}
func WithTransactionMode(mode TransactionMode) RunOption {
	return func(c *runConfig) error { c.mode = mode; return nil }
}
func WithRunState(state data.Map) RunOption {
	return func(c *runConfig) error { c.state = cloneMap(state); return nil }
}
func config(options []RunOption) (runConfig, error) {
	c := runConfig{mode: PrepareAtomic}
	for _, o := range options {
		if err := o(&c); err != nil {
			return c, err
		}
	}
	switch c.mode {
	case PrepareAtomic, CaseRollback, ExplicitNonAtomic:
	case CallerTransaction:
		if c.tx == nil {
			return c, fmt.Errorf("caller transaction requires WithTransaction")
		}
	default:
		return c, fmt.Errorf("invalid transaction mode %q", c.mode)
	}
	return c, nil
}
func cloneMap(m data.Map) data.Map {
	v := cloneValue(map[string]any(m))
	if v == nil {
		return data.Map{}
	}
	return data.Map(v.(map[string]any))
}
func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		r := make(map[string]any, len(x))
		for k, v := range x {
			r[k] = cloneValue(v)
		}
		return r
	case data.Map:
		return cloneValue(map[string]any(x))
	case []map[string]any:
		r := make([]map[string]any, len(x))
		for i, v := range x {
			r[i] = cloneValue(v).(map[string]any)
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = cloneValue(v)
		}
		return r
	case []byte:
		return bytes.Clone(x)
	default:
		return v
	}
}
func (e *Engine) runState(local data.Map, c runConfig) data.Map {
	r := cloneMap(e.state)
	for _, src := range []data.Map{local, c.state} {
		for k, v := range src {
			r[k] = cloneValue(v)
		}
	}
	registerUDFs(r)
	return r
}
func (e *Engine) Ping(ctx context.Context) error { return e.db.PingContext(ctx) }

func (e *Engine) sqlxDialect() (*info.Dialect, error) {
	name := map[string]string{"sqlite": "SQLite", "postgres": "PostgreSQL", "postgresql": "PostgreSQL", "mysql": "MySQL", "bigquery": "BigQuery", "ansi": "ANSI"}[e.dialect]
	if name == "" {
		name = e.dialect
	}
	d := registry.LookupDialect(&database.Product{Name: name})
	if d == nil {
		return nil, fmt.Errorf("SQLX dialect %q is not registered: import its metadata/product package", name)
	}
	return d, nil
}

// generatedDatabase keeps one Engine's dialect/capability policy on one driver
// family. Cross-driver Query/Compare requests use authored SQL and remain valid.
func (e *Engine) generatedDatabase(name string) (*sql.DB, error) {
	db, err := e.database(name)
	if err != nil {
		return nil, err
	}
	if reflect.TypeOf(db.Driver()) != reflect.TypeOf(e.db.Driver()) {
		return nil, fmt.Errorf("named database %q uses a different driver; generated SQL requires a separate Engine with its dialect/capabilities", name)
	}
	return db, nil
}
