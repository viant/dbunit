package dbunit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/viant/structology"
	"reflect"
	"strconv"
	"time"
)

// SQL metadata controls conversion. Text columns never gain numeric coercion.
// Timestamp text accepts RFC3339Nano, SQL datetime with optional fractional
// seconds, and ISO calendar dates. Unzoned SQL text is interpreted in UTC.
func coerceProjectedRows(rows structology.Maps, fields []structology.MapField) error {
	for i, row := range rows {
		if err := coerceRecord(row, fields); err != nil {
			return fmt.Errorf("row %d: %w", i+1, err)
		}
	}
	return nil
}
func coerceRecord(row map[string]any, fields []structology.MapField) error {
	for _, f := range fields {
		value, present := row[f.Key]
		if !present || value == nil {
			continue
		}
		if len(f.Fields) > 0 {
			if f.Repeated {
				values := reflect.ValueOf(value)
				if values.Kind() != reflect.Slice {
					return fmt.Errorf("column %q requires repeated records", f.Key)
				}
				for i := 0; i < values.Len(); i++ {
					raw := values.Index(i).Interface()
					if raw == nil {
						continue
					}
					record, ok := raw.(map[string]any)
					if !ok {
						return fmt.Errorf("column %q element %d is not an object", f.Key, i)
					}
					if err := coerceRecord(record, f.Fields); err != nil {
						return fmt.Errorf("column %q element %d: %w", f.Key, i, err)
					}
				}
			} else {
				record, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("column %q is not an object", f.Key)
				}
				if err := coerceRecord(record, f.Fields); err != nil {
					return fmt.Errorf("column %q: %w", f.Key, err)
				}
			}
			continue
		}
		if f.Tag.Get("dbunit") == "decimal" {
			if n, ok := value.(json.Number); ok {
				if _, valid := number(n); !valid {
					return fmt.Errorf("column %q invalid decimal", f.Key)
				}
				row[f.Key] = n.String()
				continue
			}
		}
		text, ok := value.(string)
		if !ok || f.Type == nil {
			continue
		}
		typ := f.Type
		for typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
		var converted any
		var err error
		if typ == reflect.TypeOf([]byte(nil)) {
			decoded, err := base64.StdEncoding.DecodeString(text)
			if err != nil {
				return fmt.Errorf("column %q requires base64 binary text: %w", f.Key, err)
			}
			row[f.Key] = decoded
			continue
		}
		if typ == reflect.TypeOf(time.Time{}) {
			converted, err = parseSQLTime(text)
		} else {
			switch typ.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				converted, err = strconv.ParseInt(text, 10, typ.Bits())
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				converted, err = strconv.ParseUint(text, 10, typ.Bits())
			case reflect.Float32, reflect.Float64:
				converted, err = strconv.ParseFloat(text, typ.Bits())
			case reflect.Bool:
				converted, err = strconv.ParseBool(text)
			default:
				continue
			}
		}
		if err != nil {
			return fmt.Errorf("column %q: %w", f.Key, err)
		}
		row[f.Key] = converted
	}
	return nil
}

func parseSQLTime(text string) (time.Time, error) {
	var parsed time.Time
	var err error
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02"} {
		parsed, err = time.Parse(layout, text)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, err
}
