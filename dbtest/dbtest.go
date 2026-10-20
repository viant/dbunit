// Package dbtest integrates dbunit reports with Go's testing package.
package dbtest

import (
	"github.com/viant/dbunit"
	"testing"
)

// Require fails the test for operation errors or assertion mismatches.
func Require(t testing.TB, report *dbunit.Report, err error) {
	t.Helper()
	if err == nil && report != nil {
		err = report.Err()
	}
	if err == nil && report != nil && report.Failed > 0 {
		err = &dbunit.AssertionError{Report: report}
	}
	if err != nil {
		if report != nil {
			for i, m := range report.Mismatches {
				if i >= 10 {
					t.Logf("%d additional mismatches omitted", len(report.Mismatches)-i)
					break
				}
				t.Logf("%s %s [%s]: expected=%v actual=%v %s", m.Expectation, m.Path, m.Category, m.Expected, m.Actual, m.Message)
			}
		}
		t.Fatalf("dbunit: %v", err)
	}
}
