package render

import (
	"encoding/json"
	"io"
)

// JSON writes v as indented JSON followed by a newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// NDJSONLine writes v as a single compact JSON line. encoding/json escapes
// newlines inside strings, so each value is guaranteed to occupy one line.
func NDJSONLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
