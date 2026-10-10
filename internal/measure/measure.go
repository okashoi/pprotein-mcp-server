// Package measure is the schema of the annotation a benchmark run is recorded with.
//
// pprotein stores a memo as free text, so the annotation travels as JSON inside one. The
// schema field keeps it apart from memos written by hand and from later versions of this
// format.
package measure

import (
	"encoding/json"
	"fmt"
	"strings"
)

const Schema = "pprotein-measure/1"

// Measure is what a benchmark run is annotated with. The commit the run measured is not
// part of it: pprotein records that itself, as reported by the measured servers.
type Measure struct {
	Schema  string   `json:"schema"`
	Comment string   `json:"comment"`
	Score   *float64 `json:"score,omitempty"`
	Pass    *bool    `json:"pass,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// Text renders the memo body carrying the measure.
func (m Measure) Text() (string, error) {
	m.Schema = Schema

	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("failed to marshal measure: %w", err)
	}

	return string(data), nil
}

// Parse reads a memo body written by Text. A memo written by hand is not a measure.
func Parse(text string) (Measure, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return Measure{}, false
	}

	var m Measure
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return Measure{}, false
	}

	if m.Schema != Schema {
		return Measure{}, false
	}

	return m, true
}
