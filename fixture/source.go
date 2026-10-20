// Package fixture loads explicitly selected test data. Paths are always relative
// to a caller-declared root; test names and caller source locations are ignored.
package fixture

import (
	"context"
	"fmt"
	"reflect"

	"github.com/viant/structology"
)

// Source describes one input. Value is reserved for in-process typed slices.
// Use an explicit kind for an empty maps/bytes input in serialized requests.
type Source struct {
	// Expanded marks concrete values produced by hydration/mapping. Consumers
	// must not evaluate toolbox templates in these records a second time.
	Expanded  bool             `json:"expanded,omitempty" yaml:"expanded,omitempty"`
	Kind      string           `json:"kind,omitempty" yaml:"kind,omitempty"`
	Path      string           `json:"path,omitempty" yaml:"path,omitempty"`
	URL       string           `json:"url,omitempty" yaml:"url,omitempty"`
	Format    string           `json:"format,omitempty" yaml:"format,omitempty"`
	Data      []map[string]any `json:"data,omitempty" yaml:"data,omitempty"`
	Value     any              `json:"-" yaml:"-"`
	Content   []byte           `json:"content,omitempty" yaml:"content,omitempty"`
	NullToken string           `json:"nullToken,omitempty" yaml:"nullToken,omitempty"`
}

func File(name string) Source           { return Source{Kind: "file", Path: name} }
func URL(location string) Source        { return Source{Kind: "url", URL: location} }
func Maps(rows []map[string]any) Source { return Source{Kind: "maps", Data: rows} }

// Resolved wraps concrete rows without evaluating their string values again.
func Resolved(rows []map[string]any) Source {
	source := Maps(rows)
	source.Expanded = true
	return source
}
func Typed(rows any) Source { return Source{Kind: "typed", Value: rows} }
func Bytes(content []byte, format string) Source {
	return Source{Kind: "bytes", Content: content, Format: format}
}

// Resolver is safe for concurrent reads when its caller-supplied FS/AFS service is.
// Load returns fresh maps on every call. Read reads raw schema or fixture bytes.
type Resolver interface {
	Load(context.Context, Source) (structology.Maps, error)
	Read(context.Context, Source) ([]byte, error)
}

// Inline resolves in-memory sources and rejects paths without an explicit root.
func Inline() Resolver { return &resolver{} }

func (s Source) kind() (string, error) {
	kind := s.Kind
	count := 0
	if s.Path != "" {
		count++
		if kind == "" {
			kind = "file"
		}
	}
	if s.URL != "" {
		count++
		if kind == "" {
			kind = "url"
		}
	}
	if s.Data != nil {
		count++
		if kind == "" {
			kind = "maps"
		}
	}
	if s.Value != nil {
		count++
		if kind == "" {
			kind = "typed"
		}
	}
	if s.Content != nil {
		count++
		if kind == "" {
			kind = "bytes"
		}
	}
	if count > 1 {
		return "", fmt.Errorf("fixture must specify exactly one source")
	}
	switch kind {
	case "file":
		if s.Path == "" || s.URL != "" || s.Data != nil || s.Value != nil || s.Content != nil {
			return "", fmt.Errorf("file fixture requires only path")
		}
	case "url":
		if s.URL == "" || s.Path != "" || s.Data != nil || s.Value != nil || s.Content != nil {
			return "", fmt.Errorf("URL fixture requires only url")
		}
	case "maps":
		if s.Path != "" || s.URL != "" || s.Value != nil || s.Content != nil {
			return "", fmt.Errorf("maps fixture has incompatible source fields")
		}
	case "bytes":
		if s.Path != "" || s.URL != "" || s.Value != nil || s.Data != nil {
			return "", fmt.Errorf("bytes fixture has incompatible source fields")
		}
	case "typed":
		if s.Value == nil || s.Path != "" || s.URL != "" || s.Data != nil || s.Content != nil {
			return "", fmt.Errorf("typed fixture requires only value")
		}
	default:
		return "", fmt.Errorf("missing or unsupported fixture kind %q", kind)
	}
	return kind, nil
}

// Clone copies maps and slices recursively, retaining scalar types such as
// json.Number and time.Time. Excessively deep/cyclic containers are rejected.
func Clone(rows []map[string]any) (structology.Maps, error) {
	out := make(structology.Maps, len(rows))
	for i, row := range rows {
		if row == nil {
			return nil, fmt.Errorf("fixture row %d is null", i+1)
		}
		v, err := cloneValue(reflect.ValueOf(row), 0)
		if err != nil {
			return nil, fmt.Errorf("fixture row %d: %w", i+1, err)
		}
		out[i] = v.Interface().(map[string]any)
	}
	return out, nil
}
func cloneValue(v reflect.Value, depth int) (reflect.Value, error) {
	if depth > 64 {
		return reflect.Value{}, fmt.Errorf("fixture exceeds nesting limit (64) or contains a cycle")
	}
	if !v.IsValid() {
		return v, nil
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		value, err := cloneValue(v.Elem(), depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(value)
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			value, err := cloneValue(iter.Value(), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(iter.Key(), value)
		}
		return out, nil
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			value, err := cloneValue(v.Index(i), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(value)
		}
		return out, nil
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			value, err := cloneValue(v.Index(i), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(value)
		}
		return out, nil
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		value, err := cloneValue(v.Elem(), depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(value)
		return out, nil
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return reflect.Value{}, fmt.Errorf("unsupported fixture value %s", v.Type())
	default:
		return v, nil
	}
}
