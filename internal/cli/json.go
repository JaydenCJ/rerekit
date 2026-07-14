// JSON output helpers. Every JSON document carries "tool" and
// "schema_version" so scripts can gate on the format they expect, and
// encoding is deterministic (sorted keys, two-space indent, trailing
// newline).
package cli

import (
	"encoding/json"
	"fmt"
)

// printJSON writes v as indented JSON followed by a newline.
func (c *ctx) printJSON(v any) int {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return c.errf("%v", err)
	}
	fmt.Fprintln(c.stdout, string(data))
	return ExitOK
}

// jsonSlice guarantees a JSON array (never null) for a possibly-nil
// slice.
func jsonSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
