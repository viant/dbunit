package dbunit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/structology"
	"time"
)

func (e *Engine) Compile(ctx context.Context, c Case) (*Plan, error) {
	copy := c
	copy.State = cloneMap(c.State)
	copy.Schema = append([]Statement(nil), c.Schema...)
	copy.Cleanup = append([]Statement(nil), c.Cleanup...)
	for i := range copy.Schema {
		copy.Schema[i].Args = cloneValue(copy.Schema[i].Args).([]any)
	}
	for i := range copy.Cleanup {
		copy.Cleanup[i].Args = cloneValue(copy.Cleanup[i].Args).([]any)
	}
	copy.Prepare = append([]Dataset(nil), c.Prepare...)
	copy.Expect = append([]Expectation(nil), c.Expect...)
	if _, err := orderDatasets(copy.Prepare); err != nil {
		return nil, err
	}
	for i := range copy.Prepare {
		d := &copy.Prepare[i]
		if _, err := e.quote(d.Table); err != nil {
			return nil, err
		}
		if _, err := e.generatedDatabase(d.Database); err != nil {
			return nil, err
		}
		rows, err := e.resolver().Load(ctx, d.Source)
		if err != nil {
			return nil, err
		}
		expanded := d.Source.Expanded
		d.Source = fixture.Maps(rows)
		d.Source.Expanded = expanded
		d.Table = append(Identifier(nil), d.Table...)
		d.Keys = append([]string(nil), d.Keys...)
		d.DependsOn = append([]string(nil), d.DependsOn...)
		d.Columns = append([]ColumnSpec(nil), d.Columns...)
		for j := range d.Columns {
			d.Columns[j].Fields = cloneMapFields(d.Columns[j].Fields)
		}
	}
	for i := range copy.Expect {
		ex := &copy.Expect[i]
		if (len(ex.Table) == 0) == (ex.Query == nil) {
			return nil, fmt.Errorf("expectation requires exactly one table or query")
		}
		if ex.Scope != "" && ex.Scope != Full && ex.Scope != Keys && ex.Scope != Query {
			return nil, fmt.Errorf("unknown scope %q", ex.Scope)
		}
		if ex.Scope == Query && ex.Query == nil {
			return nil, fmt.Errorf("query scope requires query")
		}
		if ex.Scope == Keys && (ex.Query != nil || len(ex.Match.Keys) == 0) {
			return nil, fmt.Errorf("keys scope requires table and concrete keys")
		}
		if ex.Match.Ordered && len(ex.Match.Keys) > 0 {
			return nil, fmt.Errorf("ordered and keyed matching are mutually exclusive")
		}
		if ex.Match.Ordered && ex.Query != nil && !hasOrderBy(ex.Query.SQL) {
			return nil, fmt.Errorf("ordered query requires outer ORDER BY")
		}
		if ex.Match.Ordered && ex.Query == nil && len(ex.Match.OrderBy) == 0 {
			return nil, fmt.Errorf("ordered comparison requires OrderBy")
		}
		if ex.Query == nil {
			if _, err := e.generatedDatabase(ex.Database); err != nil {
				return nil, err
			}
		}
		if _, err := e.database(ex.Database); err != nil {
			return nil, err
		}
		rows, err := e.resolver().Load(ctx, ex.Expected)
		if err != nil {
			return nil, err
		}
		expanded := ex.Expected.Expanded
		ex.Expected = fixture.Maps(rows)
		ex.Expected.Expanded = expanded
		ex.Table = append(Identifier(nil), ex.Table...)
		ex.Match.JSONColumns = append([]string(nil), ex.Match.JSONColumns...)
		ex.Match.Keys = append([]string(nil), ex.Match.Keys...)
		ex.Match.OrderBy = append([]string(nil), ex.Match.OrderBy...)
		if ex.Query != nil {
			q := *ex.Query
			q.Args = cloneValue(q.Args).([]any)
			ex.Query = &q
		}
	}
	return &Plan{definition: copy}, nil
}
func (e *Engine) RunPlan(ctx context.Context, p *Plan, options ...RunOption) (*Report, error) {
	if p == nil {
		return nil, fmt.Errorf("nil plan")
	}
	return e.Run(ctx, p.definition, options...)
}
func (e *Engine) Run(ctx context.Context, definition Case, options ...RunOption) (report *Report, err error) {
	started := time.Now()
	report = &Report{Name: definition.Name}
	c, err := configRun(options)
	if err != nil {
		return report, err
	}
	compiled, compileErr := e.Compile(ctx, definition)
	if compileErr != nil {
		report.failure = compileErr
		return report, compileErr
	}
	definition = compiled.definition
	state := e.runState(definition.State, c)
	run := &Run{engine: e, State: state, Executor: e.db, tx: c.tx, mode: c.mode}
	if c.tx != nil {
		run.Executor = c.tx
	}
	var rollback bool
	defer func() {
		if rollback {
			if rerr := run.tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
				err = errors.Join(err, rerr)
			}
			if report.Preparation != nil {
				report.Preparation.Transaction = "rolledBack"
			}
		}
		cleanupCtx := ctx
		var cancel context.CancelFunc
		if ctx.Err() != nil {
			cleanupCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
		}
		for _, s := range definition.Cleanup {
			ex, _, exErr := e.executor(s.Database, runConfig{})
			if exErr == nil {
				_, exErr = ex.ExecContext(cleanupCtx, s.SQL, s.Args...)
			}
			if exErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup: %w", exErr))
			}
		}
		report.Duration = time.Since(started)
		report.failure = err
	}()
	if c.mode == CaseRollback {
		for _, d := range definition.Prepare {
			if d.Database != "" {
				return report, fmt.Errorf("caseRollback requires default database")
			}
		}
		for _, ex := range definition.Expect {
			if ex.Database != "" {
				return report, fmt.Errorf("caseRollback requires default database")
			}
		}
	}
	inputs, err := e.prepareInputs(ctx, PrepareRequest{Datasets: definition.Prepare}, state)
	if err != nil {
		return report, err
	}
	for _, statement := range definition.Schema {
		ex, _, execErr := e.executor(statement.Database, c)
		if execErr != nil {
			return report, execErr
		}
		if _, err = ex.ExecContext(ctx, statement.SQL, statement.Args...); err != nil {
			return report, err
		}
	}
	if c.mode == CaseRollback {
		if c.tx != nil {
			return report, fmt.Errorf("caseRollback cannot own caller transaction")
		}
		tx, txErr := e.db.BeginTx(ctx, nil)
		if txErr != nil {
			return report, txErr
		}
		run.tx = tx
		run.Executor = tx
		rollback = true
		c.tx = tx
	}
	report.Preparation, err = e.executePrepare(ctx, inputs, c)
	if err != nil {
		return report, err
	}
	if definition.Act != nil {
		if err = definition.Act(ctx, run); err != nil {
			return report, err
		}
	}
	expectOptions := []RunOption{WithRunState(run.State)}
	if run.tx != nil {
		expectOptions = append(expectOptions, WithTransaction(run.tx))
	}
	expectReport, err := e.Expect(ctx, ExpectRequest{Expectations: definition.Expect}, expectOptions...)
	if expectReport != nil {
		report.Passed = expectReport.Passed
		report.Failed = expectReport.Failed
		report.Mismatches = expectReport.Mismatches
	}
	if _, ok := err.(*AssertionError); ok {
		err = &AssertionError{Report: report}
	}
	return report, err
}
func (r *Run) Query(ctx context.Context, req QueryRequest) (*QueryResult, error) {
	opts := []RunOption{WithRunState(r.State)}
	if r.tx != nil {
		opts = append(opts, WithTransaction(r.tx))
	}
	return r.engine.Query(ctx, req, opts...)
}

func cloneMapFields(fields []structology.MapField) []structology.MapField {
	if fields == nil {
		return nil
	}
	out := append([]structology.MapField(nil), fields...)
	for i := range out {
		out[i].Fields = cloneMapFields(out[i].Fields)
	}
	return out
}
