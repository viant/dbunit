package dbunit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/viant/dbunit/fixture"
	sqlxconfig "github.com/viant/sqlx/io/config"
	"github.com/viant/sqlx/io/insert"
	sqlxload "github.com/viant/sqlx/io/load"
	"github.com/viant/sqlx/loption"
	"github.com/viant/sqlx/option"
	"github.com/viant/structology"
	"github.com/viant/toolbox/data"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type preparedDataset struct {
	spec   Dataset
	rows   structology.Maps
	reason string
}

func (e *Engine) resolver() fixture.Resolver {
	if e.fixtures != nil {
		return e.fixtures
	}
	return fixture.Inline()
}
func (e *Engine) prepareInputs(ctx context.Context, req PrepareRequest, state data.Map) ([]preparedDataset, error) {
	ordered, err := orderDatasets(req.Datasets)
	if err != nil {
		return nil, err
	}
	result := make([]preparedDataset, 0, len(ordered))
	database := ""
	for i, d := range ordered {
		if _, err = e.generatedDatabase(d.Database); err != nil {
			return nil, err
		}
		if i == 0 {
			database = d.Database
		} else if d.Database != database {
			return nil, fmt.Errorf("preparation across databases requires separate requests")
		}
		if _, err = e.quote(d.Table); err != nil {
			return nil, err
		}
		if d.Operation == "" {
			d.Operation = Insert
		}
		switch d.Operation {
		case Insert, Replace:
		case Upsert, DeleteMatching:
			if len(d.Keys) == 0 {
				return nil, fmt.Errorf("%s requires keys", d.Operation)
			}
		default:
			return nil, fmt.Errorf("unsupported operation %q", d.Operation)
		}
		if d.Load.Strategy != "" && d.Load.Strategy != Auto && d.Load.Strategy != Batch && d.Load.Strategy != BulkRequired {
			return nil, fmt.Errorf("unknown load strategy %q", d.Load.Strategy)
		}
		if d.Load.BatchSize < 0 {
			return nil, fmt.Errorf("negative batch size")
		}
		rows, err := e.resolver().Load(ctx, d.Source)
		if err != nil {
			return nil, &OperationError{Operation: "load", Dataset: d.Name, Err: err}
		}
		if len(rows) > 0 && d.Load.Strategy == BulkRequired && e.native == nil {
			return nil, fmt.Errorf("native load requires WithNativeLoad with an explicitly certified capability")
		}
		for j, row := range rows {
			if !d.Source.Expanded {
				expanded, err := expandValue(state, row, false)
				if err != nil {
					return nil, &OperationError{Operation: "expand", Dataset: d.Name, Row: j + 1, Err: err}
				}
				rows[j] = expanded.(map[string]any)
			}
			if len(rows[j]) == 0 {
				return nil, fmt.Errorf("dataset %s row %d is empty: default-only rows require an explicit supported mode", d.Name, j+1)
			}
			for _, key := range d.Keys {
				value, ok := rows[j][key]
				if !ok || !concrete(value) {
					return nil, fmt.Errorf("dataset %s row %d has missing or nonconcrete key %q", d.Name, j+1, key)
				}
			}
		}
		result = append(result, preparedDataset{spec: d, rows: rows})
	}
	return result, nil
}
func orderDatasets(input []Dataset) ([]Dataset, error) {
	names := map[string]int{}
	for i, d := range input {
		if d.Name != "" {
			if _, ok := names[d.Name]; ok {
				return nil, fmt.Errorf("duplicate dataset name %q", d.Name)
			}
			names[d.Name] = i
		}
	}
	visited := make([]int, len(input))
	var out []Dataset
	var visit func(int) error
	visit = func(i int) error {
		if visited[i] == 2 {
			return nil
		}
		if visited[i] == 1 {
			return fmt.Errorf("dataset dependency cycle at %q", input[i].Name)
		}
		visited[i] = 1
		for _, dep := range input[i].DependsOn {
			j, ok := names[dep]
			if !ok {
				return fmt.Errorf("unknown dependency %q", dep)
			}
			if err := visit(j); err != nil {
				return err
			}
		}
		visited[i] = 2
		out = append(out, input[i])
		return nil
	}
	for i := range input {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (e *Engine) Prepare(ctx context.Context, req PrepareRequest, options ...RunOption) (result *PrepareResult, err error) {
	c, err := configRun(options)
	if err != nil {
		return nil, err
	}
	if c.mode == CaseRollback {
		return nil, fmt.Errorf("caseRollback requires Run")
	}
	inputs, err := e.prepareInputs(ctx, req, e.runState(req.State, c))
	if err != nil {
		return nil, err
	}
	return e.executePrepare(ctx, inputs, c)
}
func configRun(options []RunOption) (runConfig, error) { return config(options) }
func (e *Engine) executePrepare(ctx context.Context, inputs []preparedDataset, c runConfig) (result *PrepareResult, err error) {
	result = &PrepareResult{Transaction: "none"}
	if len(inputs) == 0 {
		return result, nil
	}
	for i := range inputs {
		d := &inputs[i]
		if !e.useNative(d.spec, len(d.rows)) {
			continue
		}
		if capErr := e.nativeSupport(d.spec, d.rows, c.mode); capErr != nil {
			if d.spec.Load.Strategy == BulkRequired {
				return result, capErr
			}
			d.spec.Load.Strategy = Batch
			d.reason = "batch selected before writes: " + capErr.Error()
		}
	}
	database := inputs[0].spec.Database
	ex, db, err := e.executor(database, c)
	if err != nil {
		return result, err
	}
	tx := c.tx
	owned := false
	if c.mode != ExplicitNonAtomic && tx == nil {
		if e.dialect != "sqlite" && e.dialect != "postgres" && e.dialect != "postgresql" && e.dialect != "mysql" {
			return result, fmt.Errorf("atomic preparation is not certified for %s", e.dialect)
		}
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			return result, err
		}
		owned = true
		ex = tx
		result.Transaction = "active"
	} else if tx != nil {
		result.Transaction = "callerOwned"
	} else {
		result.Transaction = "nonAtomic"
	}
	defer func() {
		if err != nil && owned {
			rollback := tx.Rollback()
			result.Transaction = "rolledBack"
			if rollback != nil && !errors.Is(rollback, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rollback))
			}
		}
	}()
	// Clear dependent tables before parents, then insert parents before children.
	for i := len(inputs) - 1; i >= 0; i-- {
		d := inputs[i]
		if d.spec.Operation != Replace {
			continue
		}
		table, _ := e.quote(d.spec.Table)
		deleted, deleteErr := ex.ExecContext(ctx, "DELETE FROM "+table)
		step := StepResult{Operation: "delete", Name: d.spec.Name, Table: d.spec.Table}
		if deleted != nil {
			step.Rows, _ = deleted.RowsAffected()
		}
		if deleteErr != nil {
			step.Error = deleteErr.Error()
		}
		result.Steps = append(result.Steps, step)
		if deleteErr != nil {
			err = deleteErr
			return result, &OperationError{Operation: "replace", Dataset: d.spec.Name, Err: err}
		}
	}
	for _, input := range inputs {
		started := time.Now()
		d := input.spec
		step := StepResult{Operation: string(d.Operation), Name: d.Name, Table: d.Table, Strategy: Batch, Reason: "presence-preserving SQLX batch insertion"}
		if input.reason != "" {
			step.Reason = input.reason
		}
		var count int64
		switch d.Operation {
		case Insert, Replace:
			if e.useNative(d, len(input.rows)) {
				step.Strategy = BulkRequired
				step.Reason = "explicitly enabled SQLX native loader"
				count, err = e.nativeRows(ctx, db, ex, tx, d, input.rows)
			} else {
				count, err = e.insertRows(ctx, db, ex, tx, d, input.rows)
			}
		case DeleteMatching:
			count, err = e.deleteRows(ctx, ex, d, input.rows)
		case Upsert:
			count, err = e.upsertRows(ctx, db, ex, tx, d, input.rows)
		}
		step.Rows = count
		step.Duration = time.Since(started)
		if err != nil {
			step.Error = err.Error()
		}
		result.Steps = append(result.Steps, step)
		result.Rows += count
		if err != nil {
			return result, &OperationError{Operation: string(d.Operation), Dataset: d.Name, Err: err}
		}
	}
	if owned {
		if err = tx.Commit(); err != nil {
			result.Transaction = "commitUnknown"
			owned = false
			return result, err
		}
		result.Transaction = "committed"
	}
	return result, nil
}
func (e *Engine) columnFields(ctx context.Context, ex Executor, d Dataset) (map[string]structology.MapField, error) {
	table, _ := e.quote(d.Table)
	rows, err := ex.QueryContext(ctx, "SELECT * FROM "+table+" WHERE 1=0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := map[string]structology.MapField{}
	for _, c := range columns {
		var t reflect.Type
		name := strings.ToUpper(c.DatabaseTypeName())
		switch {
		case strings.Contains(name, "INT"):
			t = reflect.TypeOf((*int64)(nil))
		case strings.Contains(name, "BOOL"):
			t = reflect.TypeOf((*bool)(nil))
		case strings.Contains(name, "REAL") || strings.Contains(name, "FLOAT") || strings.Contains(name, "DOUBLE"):
			t = reflect.TypeOf((*float64)(nil))
		case strings.Contains(name, "CHAR") || strings.Contains(name, "TEXT") || strings.Contains(name, "CLOB") || strings.Contains(name, "JSON"):
			t = reflect.TypeOf((*string)(nil))
		case strings.Contains(name, "BLOB") || strings.Contains(name, "BINARY"):
			t = reflect.TypeOf([]byte(nil))
		case strings.Contains(name, "DECIMAL") || strings.Contains(name, "NUMERIC"):
			t = reflect.TypeOf((*string)(nil))
		case name == "TIME" || name == "TIMETZ" || strings.HasPrefix(name, "TIME "):
			t = reflect.TypeOf((*string)(nil))
		case strings.Contains(name, "TIME") || strings.Contains(name, "DATE"):
			t = reflect.TypeOf((*time.Time)(nil))
		default:
			t = c.ScanType()
			if t == nil || strings.HasPrefix(t.String(), "sql.Null") {
				t = reflect.TypeOf((*any)(nil)).Elem()
			}
		}
		f := structology.MapField{Key: c.Name(), Type: t}
		if strings.Contains(name, "DECIMAL") || strings.Contains(name, "NUMERIC") {
			f.Tag = reflect.StructTag("sqlx:" + strconv.Quote(c.Name()) + " json:" + strconv.Quote(c.Name()) + ` dbunit:"decimal"`)
		}
		out[c.Name()] = f
	}
	for _, c := range d.Columns {
		f, ok := out[c.Name]
		if !ok {
			return nil, fmt.Errorf("unknown column override %q", c.Name)
		}
		if c.Type != nil {
			f.Type = c.Type
		}
		if len(c.Fields) > 0 {
			f.Fields = c.Fields
			f.Type = nil
			f.Repeated = c.Repeated
		}
		out[c.Name] = f
	}
	return out, nil
}
func (e *Engine) insertRows(ctx context.Context, db *sql.DB, ex Executor, tx *sql.Tx, d Dataset, rows structology.Maps) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	fields, err := e.columnFields(ctx, ex, d)
	if err != nil {
		return 0, err
	}
	table, _ := e.quote(d.Table)
	dialect, err := e.sqlxDialect()
	if err != nil {
		return 0, fmt.Errorf("SQLX dialect: import its metadata/product registration: %w", err)
	}
	plans := map[string]*structology.MapPlan{}
	services := map[string]*insert.Service{}
	var total int64
	for start := 0; start < len(rows); {
		keys := sortedKeys(rows[start])
		for _, k := range keys {
			if _, ok := fields[k]; !ok {
				return total, fmt.Errorf("unknown column %q", k)
			}
		}
		end := start + 1
		signature := strings.Join(keys, "\x00")
		for end < len(rows) && strings.Join(sortedKeys(rows[end]), "\x00") == signature {
			end++
		}
		projection := make([]structology.MapField, len(keys))
		batchRows := make(structology.Maps, end-start)
		for i := range batchRows {
			batchRows[i] = cloneValue(rows[start+i]).(map[string]any)
		}
		for i, k := range keys {
			projection[i] = fields[k]
			for _, override := range d.Columns {
				if override.Name == k && override.JSON {
					projection[i].Type = reflect.TypeOf((*string)(nil))
					for _, row := range batchRows {
						if row[k] != nil {
							b, err := json.Marshal(row[k])
							if err != nil {
								return total, err
							}
							row[k] = string(b)
						}
					}
				}
			}
		}
		plan := plans[signature]
		if plan == nil {
			plan, err = structology.NewMapPlan(projection)
			if err != nil {
				return total, err
			}
			if len(plans) < 256 {
				plans[signature] = plan
			}
		}
		if err := coerceProjectedRows(batchRows, projection); err != nil {
			return total, err
		}
		var adapted any
		if err = plan.Load(batchRows, &adapted); err != nil {
			return total, err
		}
		size, err := chooseBatchSize(d.Load.BatchSize, len(keys), dialect.MaxPlaceholderCount())
		if err != nil {
			return total, err
		}
		service := services[signature]
		if service == nil {
			serviceOptions := []option.Option{dialect}
			if suffix, err := e.upsertSuffix(d, keys); err != nil {
				return total, err
			} else if suffix != "" {
				serviceOptions = append(serviceOptions, option.OnDuplicateKeySql(suffix))
			}
			service, err = insert.New(ctx, db, table, serviceOptions...)
			if err != nil {
				return total, err
			}
			if len(services) < 256 {
				services[signature] = service
			}
		}
		opts := []option.Option{option.BatchSize(size), dialect}
		if tx != nil {
			opts = append(opts, tx)
		}
		n, _, err := service.Exec(ctx, adapted, opts...)
		total += n
		if err != nil {
			return total, err
		}
		start = end
	}
	return total, nil
}
func sortedKeys(row map[string]any) []string {
	r := make([]string, 0, len(row))
	for k := range row {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
func (e *Engine) keyPredicate(keys []string, row map[string]any, start int) (string, []any, error) {
	parts := make([]string, len(keys))
	var args []any
	for i, k := range keys {
		col, err := e.quote(Table(k))
		if err != nil {
			return "", nil, err
		}
		v, ok := row[k]
		if !ok {
			return "", nil, fmt.Errorf("missing key %q", k)
		}
		if v == nil {
			parts[i] = col + " IS NULL"
		} else {
			args = append(args, v)
			parts[i] = col + "=" + e.bind(start+len(args))
		}
	}
	return strings.Join(parts, " AND "), args, nil
}
func (e *Engine) deleteRows(ctx context.Context, ex Executor, d Dataset, rows structology.Maps) (int64, error) {
	table, _ := e.quote(d.Table)
	var total int64
	for _, row := range rows {
		predicate, args, err := e.keyPredicate(d.Keys, row, 0)
		if err != nil {
			return total, err
		}
		r, err := ex.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+predicate, args...)
		if err != nil {
			return total, err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
func (e *Engine) upsertRows(ctx context.Context, db *sql.DB, ex Executor, tx *sql.Tx, d Dataset, rows structology.Maps) (int64, error) {
	return e.insertRows(ctx, db, ex, tx, d, rows)
}
func (e *Engine) upsertSuffix(d Dataset, columns []string) (string, error) {
	if d.Operation != Upsert {
		return "", nil
	}
	if e.dialect != "sqlite" && e.dialect != "postgres" && e.dialect != "postgresql" {
		return "", fmt.Errorf("upsert is implemented for SQLite/PostgreSQL conflict targets only")
	}
	var conflict, updates []string
	for _, key := range d.Keys {
		col, err := e.quote(Table(key))
		if err != nil {
			return "", err
		}
		conflict = append(conflict, col)
	}
	for _, column := range columns {
		key := false
		for _, candidate := range d.Keys {
			if candidate == column {
				key = true
			}
		}
		if !key {
			col, err := e.quote(Table(column))
			if err != nil {
				return "", err
			}
			updates = append(updates, col+"=excluded."+col)
		}
	}
	suffix := "DO NOTHING"
	if len(updates) > 0 {
		suffix = "DO UPDATE SET " + strings.Join(updates, ",")
	}
	return "ON CONFLICT (" + strings.Join(conflict, ",") + ") " + suffix, nil
}
func (e *Engine) useNative(d Dataset, n int) bool {
	return n > 0 && e.native != nil && (d.Load.Strategy == BulkRequired || ((d.Load.Strategy == "" || d.Load.Strategy == Auto) && n >= e.native.MinRows))
}
func (e *Engine) nativeRows(ctx context.Context, db *sql.DB, ex Executor, tx *sql.Tx, d Dataset, rows structology.Maps) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	table, err := e.quote(d.Table)
	if err != nil {
		return 0, err
	}
	dialect, err := e.sqlxDialect()
	if err != nil {
		return 0, err
	}
	if e.dialect == "postgres" || e.dialect == "postgresql" {
		table = d.Table[0]
	}
	service, err := sqlxload.New(ctx, db, table, dialect, tx)
	if err != nil {
		return 0, err
	}
	var fields map[string]structology.MapField
	if e.dialect != "bigquery" {
		fields, err = e.columnFields(ctx, ex, d)
		if err != nil {
			return 0, err
		}
	} else {
		fullPlan, inferErr := structology.InferMapPlan(rows)
		if inferErr != nil {
			return 0, inferErr
		}
		fields = map[string]structology.MapField{}
		for _, f := range fullPlan.Fields() {
			fields[f.Key] = f
		}
		for _, override := range d.Columns {
			f, ok := fields[override.Name]
			if !ok {
				return 0, fmt.Errorf("unknown column override %q", override.Name)
			}
			if override.Type != nil {
				f.Type = override.Type
			}
			if len(override.Fields) > 0 {
				f.Type = nil
				f.Fields = override.Fields
				f.Repeated = override.Repeated
			}
			fields[override.Name] = f
		}
	}
	format := e.native.Format
	if e.dialect == "bigquery" {
		format = "json"
	}
	var total int64
	for start := 0; start < len(rows); {
		keys := sortedKeys(rows[start])
		end := start + 1
		for end < len(rows) && strings.Join(keys, "\x00") == strings.Join(sortedKeys(rows[end]), "\x00") {
			end++
		}
		batch := make(structology.Maps, end-start)
		for i, row := range rows[start:end] {
			batch[i] = cloneValue(row).(map[string]any)
		}
		projection := make([]structology.MapField, len(keys))
		for i, key := range keys {
			f, ok := fields[key]
			if !ok {
				return total, fmt.Errorf("unknown native column %q", key)
			}
			projection[i] = f
			for _, override := range d.Columns {
				if override.Name == key && override.JSON {
					projection[i].Fields = nil
					projection[i].Type = reflect.TypeOf((*string)(nil))
					for _, row := range batch {
						if row[key] != nil {
							encoded, err := json.Marshal(row[key])
							if err != nil {
								return total, err
							}
							row[key] = string(encoded)
						}
					}
				}
			}
		}
		plan, err := structology.NewMapPlan(projection)
		if err != nil {
			return total, err
		}
		if err := coerceProjectedRows(batch, projection); err != nil {
			return total, err
		}
		var adapted any
		if err := plan.Load(batch, &adapted); err != nil {
			return total, err
		}
		var payload any = adapted
		if strings.EqualFold(format, "json") {
			var present structology.Maps
			if err := present.Store(adapted); err != nil {
				return total, err
			}
			payload = []map[string]any(present)
		}
		opts := []loption.Option{loption.WithFormat(format)}
		if tx != nil {
			opts = append(opts, loption.WithTransaction(tx))
		}
		n, err := service.Exec(ctx, payload, opts...)
		total += int64(n)
		if err != nil {
			return total, err
		}
		start = end
	}
	return total, nil
}

func chooseBatchSize(requested, width, limit int) (int, error) {
	if width < 1 || limit < width {
		return 0, fmt.Errorf("row exceeds dialect parameter limit")
	}
	if requested == 0 {
		requested = 100
	}
	if requested < 1 {
		return 0, fmt.Errorf("batch size must be positive")
	}
	if max := limit / width; requested > max {
		requested = max
	}
	return requested, nil
}

func (e *Engine) nativeSupport(d Dataset, rows structology.Maps, mode TransactionMode) error {
	dialect, err := e.sqlxDialect()
	if err != nil {
		return err
	}
	if sqlxconfig.LoadSession(dialect) == nil {
		return fmt.Errorf("native SQLX loader is not registered for %s", dialect.Name)
	}
	if e.dialect == "bigquery" && mode != ExplicitNonAtomic {
		return fmt.Errorf("BigQuery native load requires ExplicitNonAtomic")
	}
	if mode != ExplicitNonAtomic && !e.native.Transactional {
		return fmt.Errorf("native loader is not certified transactional; use ExplicitNonAtomic")
	}
	if d.Operation != Insert && d.Operation != Replace {
		return fmt.Errorf("native load supports Insert/Replace only")
	}
	if e.dialect == "postgres" || e.dialect == "postgresql" {
		if len(d.Table) != 1 {
			return fmt.Errorf("PostgreSQL native loader currently requires one literal table component")
		}
		for _, row := range rows {
			for key := range row {
				if key != strings.ToLower(key) {
					return fmt.Errorf("PostgreSQL native loader cannot preserve mixed-case column %q", key)
				}
			}
		}
	}
	return nil
}
