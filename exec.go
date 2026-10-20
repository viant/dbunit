package dbunit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	sqlschema "github.com/viant/sqlx/io/schema"
	"github.com/viant/toolbox/data"
	"strings"
)

// ExecRequest runs explicit SQL statements against a single connection/transaction.
// Only bound arguments are expanded; SQL text is sent as authored.
// Statements are direct INSERT/UPDATE/DELETE/MERGE/REPLACE commands; CTE-wrapped
// commands are unsupported. Statements are DML; use ApplySchema for DDL and transaction options for ownership.
type ExecRequest struct {
	Database   string      `json:"database,omitempty"`
	Statements []Statement `json:"statements"`
	State      data.Map    `json:"state,omitempty"`
}
type ExecResult struct {
	RowsAffected int64  `json:"rowsAffected"`
	Statements   int    `json:"statements"`
	Transaction  string `json:"transaction"`
}

func (e *Engine) Exec(ctx context.Context, req ExecRequest, options ...RunOption) (result *ExecResult, err error) {
	result = &ExecResult{Transaction: "none"}
	c, err := configRun(options)
	if err != nil {
		return result, err
	}
	if c.mode == CaseRollback {
		return result, fmt.Errorf("caseRollback requires Run")
	}
	state := e.runState(req.State, c)
	statements := make([]Statement, len(req.Statements))
	for i, s := range req.Statements {
		if s.Database != "" && s.Database != req.Database {
			return result, fmt.Errorf("statement %d targets a different database", i+1)
		}
		if strings.TrimSpace(s.SQL) == "" {
			return result, fmt.Errorf("statement %d SQL is required", i+1)
		}
		parts, parseErr := sqlschema.SplitSQL(s.SQL, e.dialect)
		if parseErr != nil || len(parts) != 1 {
			return result, fmt.Errorf("statement %d requires one valid SQL statement: %v", i+1, parseErr)
		}
		switch sqlVerb(s.SQL) {
		case "INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE":
		default:
			return result, fmt.Errorf("statement %d must be DML; use ApplySchema for DDL", i+1)
		}
		statements[i] = s
		statements[i].Args = make([]any, len(s.Args))
		for j, arg := range s.Args {
			statements[i].Args[j], err = expandValue(state, arg, false)
			if err != nil {
				return result, fmt.Errorf("statement %d argument %d: %w", i+1, j+1, err)
			}
		}
	}
	if len(statements) == 0 {
		return result, nil
	}
	ex, db, err := e.executor(req.Database, c)
	if err != nil {
		return result, err
	}
	tx := c.tx
	owned := false
	if tx != nil {
		result.Transaction = "callerOwned"
	} else if c.mode != ExplicitNonAtomic {
		switch e.dialect {
		case "sqlite", "postgres", "postgresql", "mysql":
		default:
			return result, fmt.Errorf("atomic SQL execution is not certified for %s", e.dialect)
		}
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			return result, err
		}
		ex = tx
		owned = true
		result.Transaction = "active"
	} else {
		conn, connErr := db.Conn(ctx)
		if connErr != nil {
			return result, connErr
		}
		defer conn.Close()
		ex = conn
		result.Transaction = "nonAtomic"
	}
	defer func() {
		if owned && err != nil {
			rollbackErr := tx.Rollback()
			result.Transaction = "rolledBack"
			if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, rollbackErr)
			}
		}
	}()
	for i, s := range statements {
		executed, execErr := ex.ExecContext(ctx, s.SQL, s.Args...)
		if execErr != nil {
			return result, fmt.Errorf("statement %d: %w", i+1, execErr)
		}
		result.Statements++
		count, countErr := executed.RowsAffected()
		if countErr != nil {
			return result, fmt.Errorf("statement %d affected rows: %w", i+1, countErr)
		}
		result.RowsAffected += count
	}
	if owned {
		owned = false
		if err = tx.Commit(); err != nil {
			result.Transaction = "commitUnknown"
			return result, err
		}
		result.Transaction = "committed"
	}
	return result, nil
}

func sqlVerb(text string) string {
	for {
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "--") {
			if end := strings.IndexByte(text, '\n'); end >= 0 {
				text = text[end+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(text, "/*") {
			depth := 1
			i := 2
			for i < len(text) && depth > 0 {
				if strings.HasPrefix(text[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(text[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return ""
			}
			text = text[i:]
			continue
		}
		break
	}
	end := 0
	for end < len(text) && ((text[end] >= 'a' && text[end] <= 'z') || (text[end] >= 'A' && text[end] <= 'Z')) {
		end++
	}
	return strings.ToUpper(text[:end])
}
