// Package jsonerr turns encoding/json errors into plain sentences that name
// a line or a key, so no Go type names reach the user (improvement spec
// §3.2 rule 1).
package jsonerr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Describe explains why data failed to decode, e.g. "invalid JSON at line
// 3" or `"history_size" must be a number`. err must come from decoding
// data.
func Describe(data []byte, err error) string {
	if se, ok := errors.AsType[*json.SyntaxError](err); ok {
		return fmt.Sprintf("invalid JSON at line %d", lineAt(data, se.Offset))
	}
	if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		if te.Field == "" {
			return fmt.Sprintf("the top level must be %s", want(te.Type))
		}
		return fmt.Sprintf("%q must be %s", te.Field, want(te.Type))
	}
	if errors.Is(err, errors.ErrUnsupported) {
		return "unsupported content"
	}
	// io.ErrUnexpectedEOF and friends: truncated input.
	return fmt.Sprintf("invalid JSON at line %d", lineAt(data, int64(len(data))))
}

// lineAt returns the 1-based line containing byte offset off.
func lineAt(data []byte, off int64) int {
	if off > int64(len(data)) {
		off = int64(len(data))
	}
	if off < 0 {
		off = 0
	}
	return bytes.Count(data[:off], []byte("\n")) + 1
}

// want names a Go type the way a JSON author thinks about it.
func want(t reflect.Type) string {
	if t == nil {
		return "a different type"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Bool:
		return "true or false"
	case reflect.String:
		return "text"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Struct, reflect.Map:
		return "an object"
	default:
		return "a different type"
	}
}
