package evaluation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// Load reads a bounded, strict UTF-8 JSON array. Errors omit file paths and
// parser excerpts because privately supplied evaluation queries may be sensitive.
func Load(path string) ([]Case, error) {
	const maxBytes = 1 << 20
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("evaluation: cases must be a readable regular file of at most 1 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("evaluation: cannot open cases file")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("evaluation: cases must be a regular file of at most 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return nil, fmt.Errorf("evaluation: cannot read cases within 1 MiB limit")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' || !utf8.Valid(data) {
		return nil, fmt.Errorf("evaluation: cases must be a UTF-8 JSON array")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var decoded []strictCase
	if err := d.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("evaluation: cannot decode cases")
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("evaluation: unexpected trailing data")
	}
	cases := make([]Case, len(decoded))
	for i, c := range decoded {
		cases[i] = Case(c)
	}
	if err := validateCases(cases); err != nil {
		return nil, err
	}
	return cases, nil
}

// Restrict special decoding to input files; report results also embed Case and
// must remain readable by ordinary JSON decoders.
type strictCase Case

// UnmarshalJSON rejects repeated fields instead of allowing a later label to
// silently overwrite an earlier one.
func (c *strictCase) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("evaluation: case must be an object")
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fmt.Errorf("evaluation: duplicate case field")
		}
		if key != "id" && key != "query" && key != "expected_ids" && key != "note" {
			return fmt.Errorf("evaluation: unknown case field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode((*Case)(c))
}
