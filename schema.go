package dbunit

import (
	"context"
	"fmt"
	"github.com/viant/sqlx/io/schema"
	"strings"
)

func (e *Engine) ApplySchema(ctx context.Context, req SchemaApplyRequest) (*SchemaResult, error) {
	db, err := e.generatedDatabase(req.Database)
	if err != nil {
		return nil, err
	}
	dialect, err := e.sqlxDialect()
	if err != nil {
		return nil, err
	}
	statements := append([]string(nil), req.Statements...)
	artifact := &schema.Artifact{Dialect: dialect.Name, Statements: statements, Complete: true}
	if req.Artifact != nil {
		if len(req.Sources) > 0 || len(req.Statements) > 0 {
			return nil, fmt.Errorf("schema artifact cannot be combined with sources/statements")
		}
		artifact.Dialect = req.Artifact.Product
		artifact.Statements = append([]string(nil), req.Artifact.Statements...)
		artifact.Complete = req.Artifact.Complete
		artifact.Checksum = req.Artifact.Checksum
		for _, omission := range req.Artifact.Omissions {
			artifact.Omissions = append(artifact.Omissions, schema.Omission{Reason: omission})
		}
		if len(artifact.Statements) == 0 && req.Artifact.SQL != "" {
			artifact.Statements, err = schema.SplitSQL(req.Artifact.SQL, dialect.Name)
			if err != nil {
				return nil, err
			}
		}
	}
	for i, source := range req.Sources {
		content, err := e.resolver().Read(ctx, source)
		if err != nil {
			return nil, &OperationError{Operation: "schema source", Dataset: fmt.Sprint(i + 1), Err: err}
		}
		parts, err := schema.SplitSQL(string(content), dialect.Name)
		if err != nil {
			return nil, fmt.Errorf("schema source %d: %w", i+1, err)
		}
		artifact.Statements = append(artifact.Statements, parts...)
	}
	atomic := strings.EqualFold(dialect.Name, "sqlite") || strings.EqualFold(dialect.Name, "postgresql")
	applied, err := schema.Apply(ctx, db, artifact, schema.ApplyOptions{Dialect: dialect, Atomic: atomic, AllowPartial: req.AllowPartial})
	result := &SchemaResult{Transaction: "nonAtomic"}
	if applied != nil {
		result.Statements = applied.Executed
		if applied.RolledBack {
			result.Transaction = "rolledBack"
		} else if applied.Atomic && err == nil {
			result.Transaction = "committed"
		}
	}
	return result, err
}
func (e *Engine) ExtractSchema(ctx context.Context, req SchemaExtractRequest) (*SchemaArtifact, error) {
	db, err := e.generatedDatabase(req.Database)
	if err != nil {
		return nil, err
	}
	dialect, err := e.sqlxDialect()
	if err != nil {
		return nil, err
	}
	native, err := schema.Extract(ctx, db, schema.ExtractOptions{Dialect: dialect, Schema: req.Schema, Tables: req.Tables, AllowPartial: req.AllowPartial, IncludeDependencies: req.IncludeDependencies, IncludeViews: req.IncludeViews, IncludeTriggers: req.IncludeTriggers, IncludeSequenceState: req.IncludeSequenceState})
	if native == nil {
		return nil, err
	}
	result := &SchemaArtifact{Product: native.Dialect, SQL: native.SQL(), Statements: append([]string(nil), native.Statements...), Complete: native.Complete, Checksum: native.Checksum}
	for _, omission := range native.Omissions {
		result.Omissions = append(result.Omissions, omission.Object+": "+omission.Feature+": "+omission.Reason)
	}
	return result, err
}
