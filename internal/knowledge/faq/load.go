package faq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const maxFileBytes = 1 << 20

// Load reads a regular JSON file once at startup. The size limit is checked both
// before and during reading; replacing or growing the file cannot bypass it.
// Errors intentionally omit paths, raw file contents, and JSON parser excerpts.
func Load(path string) (*Retriever, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: file path is blank", ErrInvalidData)
	}
	// Reject special files before opening them: opening a FIFO may otherwise
	// block forever. Recheck the opened descriptor rather than trusting this stat.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot inspect file", ErrInvalidData)
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, fmt.Errorf("%w: file must be regular and at most 1 MiB", ErrInvalidData)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open file", ErrInvalidData)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, fmt.Errorf("%w: file must be regular and at most 1 MiB", ErrInvalidData)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read file", ErrInvalidData)
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("%w: file exceeds 1 MiB", ErrInvalidData)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' || !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: file must contain a UTF-8 JSON array", ErrInvalidData)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var entries []Entry
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("%w: cannot decode FAQ entries", ErrInvalidData)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: unexpected trailing data", ErrInvalidData)
	}
	return New(entries)
}
