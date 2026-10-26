// Package native contains implementation details shared by native API packages.
package native

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

// Snapshot validates and recursively copies JSON data without losing nulls.
func Snapshot(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case bool, string:
		return v, nil
	case json.Number:
		n, e := v.Float64()
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("invalid number %q", v)
		}
		return n, nil
	case map[string]any:
		if v == nil {
			return nil, nil
		}
		out := make(map[string]any, len(v))
		for k, x := range v {
			c, e := Snapshot(x)
			if e != nil {
				return nil, fmt.Errorf("key %q: %w", k, e)
			}
			out[k] = c
		}
		return out, nil
	case []any:
		if v == nil {
			return nil, nil
		}
		out := make([]any, len(v))
		for i, x := range v {
			c, e := Snapshot(x)
			if e != nil {
				return nil, fmt.Errorf("index %d: %w", i, e)
			}
			out[i] = c
		}
		return out, nil
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := rv.Int()
		if n > 1<<53 || n < -(1<<53) {
			return nil, fmt.Errorf("integer cannot be represented exactly as JSON number")
		}
		return float64(n), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := rv.Uint()
		if n > 1<<53 {
			return nil, fmt.Errorf("integer cannot be represented exactly as JSON number")
		}
		return float64(n), nil
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.String:
		return rv.String(), nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("JSON object keys must be strings")
		}
		if rv.IsNil() {
			return nil, nil
		}
		out := make(map[string]any, rv.Len())
		it := rv.MapRange()
		for it.Next() {
			k := it.Key().String()
			v, e := Snapshot(it.Value().Interface())
			if e != nil {
				return nil, fmt.Errorf("key %q: %w", k, e)
			}
			out[k] = v
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil, nil
		}
		out := make([]any, rv.Len())
		for i := range out {
			v, e := Snapshot(rv.Index(i).Interface())
			if e != nil {
				return nil, fmt.Errorf("index %d: %w", i, e)
			}
			out[i] = v
		}
		return out, nil
	case reflect.Float32, reflect.Float64:
		n := rv.Float()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("nonfinite number")
		}
		return n, nil
	}
	return nil, fmt.Errorf("unsupported JSON type %T", value)
}
func Map(m map[string]any) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	v, e := Snapshot(m)
	if e != nil {
		return nil, e
	}
	return v.(map[string]any), nil
}
