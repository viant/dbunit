// Package dbunit prepares and validates explicit SQL database fixtures.
package dbunit

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/structology"
	"github.com/viant/toolbox/data"
	"reflect"
	"time"
)

// Identifier preserves components: Table("a.b") names one identifier containing a dot.
type Identifier []string

func (i *Identifier) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		*i = Table(one)
		return nil
	}
	var parts []string
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	*i = Table(parts...)
	return nil
}
func Table(parts ...string) Identifier { return append(Identifier(nil), parts...) }

type Operation string

const (
	Insert         Operation = "insert"
	Replace        Operation = "replace"
	Upsert         Operation = "upsert"
	DeleteMatching Operation = "deleteMatching"
)

type Strategy string

const (
	Auto         Strategy = "auto"
	Batch        Strategy = "batch"
	BulkRequired Strategy = "bulkRequired"
)

type Scope string

const (
	Full  Scope = "full"
	Keys  Scope = "keys"
	Query Scope = "query"
)

type TransactionMode string

const (
	PrepareAtomic     TransactionMode = "prepareAtomic"
	CallerTransaction TransactionMode = "callerTransaction"
	CaseRollback      TransactionMode = "caseRollback"
	ExplicitNonAtomic TransactionMode = "nonAtomic"
)

type LoadOptions struct {
	Strategy  Strategy `json:"strategy,omitempty"`
	BatchSize int      `json:"batchSize,omitempty"`
}
type ColumnSpec struct {
	Name     string                 `json:"name"`
	Type     reflect.Type           `json:"-"`
	Fields   []structology.MapField `json:"-"`
	Repeated bool                   `json:"repeated,omitempty"`
	JSON     bool                   `json:"json,omitempty"`
}
type Dataset struct {
	Name      string         `json:"name,omitempty"`
	Database  string         `json:"database,omitempty"`
	Table     Identifier     `json:"table"`
	Source    fixture.Source `json:"source"`
	Operation Operation      `json:"operation,omitempty"`
	Load      LoadOptions    `json:"load,omitempty"`
	Columns   []ColumnSpec   `json:"columns,omitempty"`
	Keys      []string       `json:"keys,omitempty"`
	DependsOn []string       `json:"dependsOn,omitempty"`
}
type MatchOptions struct {
	Keys         []string `json:"keys,omitempty"`
	Ordered      bool     `json:"ordered,omitempty"`
	OrderBy      []string `json:"orderBy,omitempty"`
	Multiset     bool     `json:"multiset,omitempty"`
	Contains     bool     `json:"contains,omitempty"`
	StrictFields bool     `json:"strictFields,omitempty"`
	AllowEmpty   bool     `json:"allowEmpty,omitempty"`
	MaxRows      int      `json:"maxRows,omitempty"`
	JSONColumns  []string `json:"jsonColumns,omitempty"`
}
type QuerySpec struct {
	SQL  string `json:"sql"`
	Args []any  `json:"args,omitempty"`
}
type Expectation struct {
	Name     string         `json:"name,omitempty"`
	Database string         `json:"database,omitempty"`
	Table    Identifier     `json:"table,omitempty"`
	Query    *QuerySpec     `json:"query,omitempty"`
	Expected fixture.Source `json:"expected"`
	Scope    Scope          `json:"scope,omitempty"`
	Match    MatchOptions   `json:"match,omitempty"`
}
type PrepareRequest struct {
	Datasets []Dataset `json:"datasets"`
	State    data.Map  `json:"state,omitempty"`
}
type ExpectRequest struct {
	Expectations []Expectation `json:"expectations"`
	State        data.Map      `json:"state,omitempty"`
}
type QueryRequest struct {
	Database string   `json:"database,omitempty"`
	SQL      string   `json:"sql"`
	Args     []any    `json:"args,omitempty"`
	State    data.Map `json:"state,omitempty"`
	MaxRows  int      `json:"maxRows,omitempty"`
}
type QueryResult struct {
	Columns []string         `json:"columns"`
	Rows    structology.Maps `json:"rows"`
}
type Statement struct {
	Database string `json:"database,omitempty"`
	SQL      string `json:"sql"`
	Args     []any  `json:"args,omitempty"`
}
type Case struct {
	Name    string
	State   data.Map
	Schema  []Statement
	Prepare []Dataset
	Act     func(context.Context, *Run) error
	Expect  []Expectation
	Cleanup []Statement
}
type Plan struct{ definition Case }
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Run struct {
	engine   *Engine
	State    data.Map
	Executor Executor
	tx       *sql.Tx
	mode     TransactionMode
}

func (r *Run) Set(path string, value any) error { r.State.SetValue(path, value); return nil }

type StepResult struct {
	Operation string        `json:"operation,omitempty"`
	Name      string        `json:"name,omitempty"`
	Table     Identifier    `json:"table,omitempty"`
	Rows      int64         `json:"rows"`
	Strategy  Strategy      `json:"strategy,omitempty"`
	Reason    string        `json:"reason,omitempty"`
	Duration  time.Duration `json:"duration,omitempty"`
	Error     string        `json:"error,omitempty"`
}
type PrepareResult struct {
	Steps       []StepResult `json:"steps"`
	Rows        int64        `json:"rows"`
	Transaction string       `json:"transaction"`
}
type Mismatch struct {
	Expectation string `json:"expectation,omitempty"`
	Path        string `json:"path"`
	Category    string `json:"category"`
	Expected    any    `json:"expected,omitempty"`
	Actual      any    `json:"actual,omitempty"`
	Message     string `json:"message,omitempty"`
}
type Report struct {
	Name        string         `json:"name,omitempty"`
	Passed      int            `json:"passed"`
	Failed      int            `json:"failed"`
	Mismatches  []Mismatch     `json:"mismatches,omitempty"`
	Preparation *PrepareResult `json:"preparation,omitempty"`
	Duration    time.Duration  `json:"duration,omitempty"`
	failure     error
}

func (r *Report) Err() error {
	if r == nil {
		return nil
	}
	return r.failure
}

type AssertionError struct{ Report *Report }

func (a *AssertionError) Error() string { return "dbunit: expectation failed" }

type OperationError struct {
	Operation, Dataset string
	Row                int
	Err                error
}

func (e *OperationError) Error() string {
	return "dbunit " + e.Operation + " " + e.Dataset + ": " + e.Err.Error()
}
func (e *OperationError) Unwrap() error { return e.Err }

type SchemaApplyRequest struct {
	Database     string           `json:"database,omitempty"`
	Sources      []fixture.Source `json:"sources,omitempty"`
	Statements   []string         `json:"statements,omitempty"`
	Artifact     *SchemaArtifact  `json:"artifact,omitempty"`
	AllowPartial bool             `json:"allowPartial,omitempty"`
}
type SchemaResult struct {
	Statements  int    `json:"statements"`
	Transaction string `json:"transaction"`
}
type SchemaExtractRequest struct {
	Database             string   `json:"database,omitempty"`
	Schema               string   `json:"schema,omitempty"`
	Tables               []string `json:"tables"`
	AllowPartial         bool     `json:"allowPartial,omitempty"`
	IncludeDependencies  bool     `json:"includeDependencies,omitempty"`
	IncludeViews         bool     `json:"includeViews,omitempty"`
	IncludeTriggers      bool     `json:"includeTriggers,omitempty"`
	IncludeSequenceState bool     `json:"includeSequenceState,omitempty"`
}
type SchemaArtifact struct {
	Product    string   `json:"product"`
	SQL        string   `json:"sql"`
	Statements []string `json:"statements"`
	Complete   bool     `json:"complete"`
	Omissions  []string `json:"omissions,omitempty"`
	Checksum   string   `json:"checksum"`
}
