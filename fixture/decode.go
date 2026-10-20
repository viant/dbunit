package fixture

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/viant/structology"
)

func decode(content []byte, format, nullToken string) (structology.Maps, error) {
	content = bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})
	switch format {
	case "json":
		d := json.NewDecoder(bytes.NewReader(content))
		d.UseNumber()
		var rows structology.Maps
		if err := d.Decode(&rows); err != nil {
			return nil, err
		}
		if rows == nil {
			return nil, fmt.Errorf("expected JSON array, got null")
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("unexpected trailing JSON data: %v", err)
		}
		for i, row := range rows {
			if row == nil {
				return nil, fmt.Errorf("row %d is null", i+1)
			}
		}
		return rows, nil
	case "ndjson", "jsonl":
		d := json.NewDecoder(bytes.NewReader(content))
		d.UseNumber()
		rows := make(structology.Maps, 0)
		for {
			var row map[string]any
			err := d.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("row %d: %w", len(rows)+1, err)
			}
			if row == nil {
				return nil, fmt.Errorf("row %d is null", len(rows)+1)
			}
			rows = append(rows, row)
		}
		return rows, nil
	case "csv", "tsv":
		reader := csv.NewReader(bytes.NewReader(content))
		if format == "tsv" {
			reader.Comma = '\t'
		}
		header, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("header: %w", err)
		}
		seen := map[string]bool{}
		for _, name := range header {
			if strings.TrimSpace(name) == "" || seen[name] {
				return nil, fmt.Errorf("empty or duplicate column %q", name)
			}
			seen[name] = true
		}
		if nullToken == "" {
			nullToken = `\N`
		}
		rows := make(structology.Maps, 0)
		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("row %d: %w", len(rows)+1, err)
			}
			row := make(map[string]any, len(header))
			for i, col := range header {
				if record[i] == nullToken {
					row[col] = nil
				} else {
					row[col] = record[i]
				}
			}
			rows = append(rows, row)
		}
		return rows, nil
	default:
		return nil, fmt.Errorf("unsupported fixture format %q; specify json, ndjson, csv or tsv", format)
	}
}
