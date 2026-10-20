package dbunit

import (
	sqlsource "github.com/viant/sqlparser/source"
	"strings"
)

// hasOrderBy checks the outer result ordering, excluding strings, comments and
// nested queries. SQL syntax and whether the ordering is total remain the
// author's contract, verified by the database when the query executes.
func hasOrderBy(sql string) bool {
	code := make([]byte, len(sql))
	for i := range code {
		code[i] = ' '
	}
	depth := 0
	scanner := sqlsource.NewCodeScanner(sql, 0)
	for at, ok := scanner.Next(); ok; at, ok = scanner.Next() {
		switch sql[at] {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 {
				code[at] = sql[at]
			}
		}
	}
	words := strings.Fields(strings.ToUpper(string(code)))
	for i := 1; i < len(words); i++ {
		if words[i-1] == "ORDER" && words[i] == "BY" {
			return true
		}
	}
	return false
}
