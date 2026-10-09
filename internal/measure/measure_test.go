package measure

import "testing"

func TestTextSetsTheSchema(t *testing.T) {
	score := 12345.0
	text, err := Measure{Comment: "add an index", Score: &score}.Text()
	if err != nil {
		t.Fatalf("Text() returned error: %v", err)
	}

	m, ok := Parse(text)
	if !ok {
		t.Fatalf("Parse() rejected the text Text() wrote: %s", text)
	}
	if m.Schema != Schema {
		t.Errorf("got schema %q, want %q", m.Schema, Schema)
	}
	if m.Comment != "add an index" {
		t.Errorf("got comment %q, want it preserved", m.Comment)
	}
	if m.Score == nil || *m.Score != score {
		t.Errorf("got score %v, want %v", m.Score, score)
	}
	if m.Pass != nil {
		t.Errorf("got pass %v, want it left out", *m.Pass)
	}
}

func TestParseRejectsWhatIsNotAMeasure(t *testing.T) {
	for name, text := range map[string]string{
		"written by hand":    "add an index",
		"json but no schema": `{"comment":"add an index"}`,
		"another schema":     `{"schema":"pprotein-measure/9","comment":"add an index"}`,
		"broken json":        `{"schema":"pprotein-measure/1",`,
		"empty":              "",
	} {
		if _, ok := Parse(text); ok {
			t.Errorf("Parse() accepted a memo %s: %s", name, text)
		}
	}
}
