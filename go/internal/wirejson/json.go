// Package wirejson rejects ambiguous JSON before converting it into Go values.
package wirejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid JSON contract")

// Decode requires exact field names, every non-omitempty field, no null values,
// duplicate/unknown properties, trailing JSON or wrong fixed-array lengths.
// Error text deliberately never includes input bytes, field values or secrets.
func Decode(data []byte, dst any) error {
	if !utf8.Valid(data) {
		return ErrInvalid
	}
	t := reflect.TypeOf(dst)
	if t == nil || t.Kind() != reflect.Pointer {
		return ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, e := parse(dec, 0)
	if e != nil {
		return ErrInvalid
	}
	if _, e = dec.Token(); e != io.EOF {
		return ErrInvalid
	}
	if !matches(value, t.Elem()) {
		return ErrInvalid
	}
	if json.Unmarshal(data, dst) != nil {
		return ErrInvalid
	}
	return nil
}
func parse(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, ErrInvalid
	}
	tok, e := d.Token()
	if e != nil || tok == nil {
		return nil, ErrInvalid
	}
	switch delim := tok.(type) {
	case json.Delim:
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				k, ok := key.(string)
				if e != nil || !ok {
					return nil, ErrInvalid
				}
				if _, exists := m[k]; exists {
					return nil, ErrInvalid
				}
				v, e := parse(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[k] = v
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, ErrInvalid
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := parse(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, ErrInvalid
			}
			return a, nil
		default:
			return nil, ErrInvalid
		}
	}
	return tok, nil
}

var timeType = reflect.TypeOf(time.Time{})
var rawType = reflect.TypeOf(json.RawMessage{})

func matches(v any, t reflect.Type) bool {
	if v == nil {
		return false
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		_, ok := v.(string)
		return ok
	}
	if t == rawType || t.Kind() == reflect.Interface {
		return true
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			parts := strings.Split(f.Tag.Get("json"), ",")
			name := parts[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			known[name] = true
			value, present := m[name]
			optional := false
			for _, p := range parts[1:] {
				optional = optional || p == "omitempty"
			}
			if !present {
				if !optional {
					return false
				}
				continue
			}
			if !matches(value, f.Type) {
				return false
			}
		}
		for k := range m {
			if !known[k] {
				return false
			}
		}
		return true
	case reflect.Array, reflect.Slice:
		a, ok := v.([]any)
		if !ok || t.Kind() == reflect.Array && len(a) != t.Len() {
			return false
		}
		for _, x := range a {
			if !matches(x, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok || t.Key().Kind() != reflect.String {
			return false
		}
		for _, x := range m {
			if !matches(x, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		_, ok := v.(string)
		return ok
	case reflect.Bool:
		_, ok := v.(bool)
		return ok
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		_, ok := v.(json.Number)
		return ok
	}
	return false
}
