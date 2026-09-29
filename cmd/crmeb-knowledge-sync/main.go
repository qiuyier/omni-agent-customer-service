// Command crmeb-knowledge-sync publishes validated full CRMEB snapshots.
// It never connects to a database or calls a model; see scripts/crmeb_knowledge_export.php.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
	nethtml "golang.org/x/net/html"
)

type article struct {
	ID              int    `json:"id"`
	CategoryID      int    `json:"category_id"`
	Title           string `json:"title"`
	Content         string `json:"content"`
	Status          *int   `json:"status"`
	Hide            *int   `json:"hide"`
	CategoryStatus  *int   `json:"category_status"`
	CategoryHidden  *int   `json:"category_hidden"`
	CategoryDeleted *int   `json:"category_deleted"`
	Marker          string `json:"marker"`
}
type snapshot struct {
	Schema   int       `json:"schema_version"`
	Source   string    `json:"source_id"`
	Complete bool      `json:"complete"`
	Count    int       `json:"record_count"`
	Exported time.Time `json:"exported_at"`
	Articles []article `json:"articles"`
}
type metadata struct {
	Source     string    `json:"source_id"`
	TestMarker string    `json:"test_marker,omitempty"`
	InputHash  string    `json:"input_sha256"`
	FAQHash    string    `json:"faq_sha256"`
	Exported   time.Time `json:"exported_at"`
	Published  time.Time `json:"published_at"`
	Count      int       `json:"entries"`
}

var sourcePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,47}$`)

func digest(b []byte) string { x := sha256.Sum256(b); return hex.EncodeToString(x[:]) }
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func readBounded(p string) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if len(b) > 16<<20 {
		return nil, errors.New("input exceeds 16 MiB")
	}
	return b, e
}

// Only text is retained. No links/images are fetched and no HTML is executed.
// This is not an instruction-injection detector; references remain untrusted.
func plain(s string) (string, error) {
	n, e := nethtml.Parse(strings.NewReader(html.UnescapeString(s)))
	if e != nil {
		return "", e
	}
	var b strings.Builder
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode {
			switch n.Data {
			case "script", "style", "iframe", "object", "svg", "template":
				return
			}
		}
		if n.Type == nethtml.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " "), nil
}
func transform(b []byte, source, marker string, keywords map[string][]string, now time.Time) ([]faq.Entry, metadata, error) {
	var s snapshot
	var m metadata
	if !sourcePattern.MatchString(source) || (marker != "" && !sourcePattern.MatchString(marker)) {
		return nil, m, errors.New("invalid source or test marker")
	}
	if err := decode(b, &s); err != nil {
		return nil, m, errors.New("invalid snapshot JSON")
	}
	if s.Schema != 1 || s.Source != source || !s.Complete || s.Articles == nil || s.Count != len(s.Articles) || s.Count > 1000 || s.Exported.IsZero() || s.Exported.After(now.Add(5*time.Minute)) || now.Sub(s.Exported) > 24*time.Hour {
		return nil, m, errors.New("incomplete, stale or mismatched snapshot")
	}
	entries := []faq.Entry{}
	seen := map[int]bool{}
	for _, a := range s.Articles {
		if a.ID <= 0 || a.CategoryID <= 0 || seen[a.ID] {
			return nil, m, errors.New("invalid or duplicate article identity")
		}
		seen[a.ID] = true
		for _, v := range []*int{a.Status, a.Hide, a.CategoryStatus, a.CategoryHidden, a.CategoryDeleted} {
			if v == nil || (*v != 0 && *v != 1) {
				return nil, m, errors.New("missing or invalid publication flags")
			}
		}
		include := *a.Status == 1 && *a.Hide == 0 && *a.CategoryStatus == 1 && *a.CategoryHidden == 0 && *a.CategoryDeleted == 0
		if marker != "" {
			include = a.Marker == marker && *a.CategoryDeleted == 0
		} else if strings.HasPrefix(a.Marker, "omni-fixture-") {
			include = false
		}
		if !include {
			continue
		}
		text, e := plain(a.Content)
		if e != nil {
			return nil, m, e
		}
		id := fmt.Sprintf("%s-article-%d", source, a.ID)
		words := keywords[fmt.Sprint(a.ID)]
		if len(words) == 0 {
			words = []string{strings.TrimSpace(a.Title)}
		}
		entries = append(entries, faq.Entry{ID: id, Title: strings.TrimSpace(a.Title), Source: fmt.Sprintf("crmeb/%s/article/%d", source, a.ID), Content: text, Keywords: words})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	if len(entries) > 0 {
		if _, e := faq.New(entries); e != nil {
			return nil, m, fmt.Errorf("FAQ validation: %w", e)
		}
	}
	m = metadata{Source: source, TestMarker: marker, InputHash: digest(b), Exported: s.Exported, Published: now, Count: len(entries)}
	return entries, m, nil
}

// Readers use output/current/faq.json. The symlink atomically switches a whole
// generation (FAQ + metadata); old generations are retained, never pruned here.
func publish(root string, entries []faq.Entry, m metadata, allowEmpty bool) (metadata, error) {
	if len(entries) == 0 && !allowEmpty {
		return m, errors.New("empty result requires -allow-empty; old generation preserved")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return m, e
	}
	lock := filepath.Join(root, ".sync-lock")
	if e := os.Mkdir(lock, 0700); e != nil {
		return m, errors.New("output locked; concurrent or interrupted sync")
	}
	defer os.Remove(lock)
	current := filepath.Join(root, "current")
	if st, e := os.Lstat(current); e == nil {
		if st.Mode()&os.ModeSymlink == 0 {
			return m, errors.New("current must be a generation symlink")
		}
		target, e := os.Readlink(current)
		if e != nil || filepath.Base(target) != target || !strings.HasPrefix(target, "generation-") {
			return m, errors.New("invalid current target")
		}
		b, e := readBounded(filepath.Join(root, target, "metadata.json"))
		if e != nil {
			return m, e
		}
		var old metadata
		if e = decode(b, &old); e != nil || old.Source != m.Source || old.TestMarker != m.TestMarker || m.Exported.Before(old.Exported) {
			return m, errors.New("source/mode mismatch or older snapshot")
		}
	} else if !os.IsNotExist(e) {
		return m, e
	}
	data, e := json.MarshalIndent(entries, "", "  ")
	if e != nil {
		return m, e
	}
	data = append(data, '\n')
	m.FAQHash = digest(data)
	meta, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return m, e
	}
	gen, e := os.MkdirTemp(root, "generation-")
	if e != nil {
		return m, e
	}
	for name, b := range map[string][]byte{"faq.json": data, "metadata.json": meta} {
		f, e := os.OpenFile(filepath.Join(gen, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return m, e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return m, e
		}
		if ce != nil {
			return m, ce
		}
	}
	pending := filepath.Join(root, ".next-"+filepath.Base(gen))
	if e = os.Symlink(filepath.Base(gen), pending); e != nil {
		return m, e
	}
	defer os.Remove(pending)
	if e = os.Rename(pending, current); e != nil {
		return m, e
	}
	return m, nil
}
func run(args []string, w io.Writer) error {
	f := flag.NewFlagSet("crmeb-knowledge-sync", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	input := f.String("input", "", "complete export JSON")
	source := f.String("source-id", "", "trusted source identifier")
	out := f.String("output", "", "private generation directory")
	marker := f.String("test-marker", "", "explicit fixture-only mode; use separate output")
	keyfile := f.String("keywords", "", "optional article-ID to keywords JSON")
	empty := f.Bool("allow-empty", false, "publish empty withdrawal snapshot (FAQ server will fail closed on next load)")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || *input == "" || *source == "" || *out == "" {
		return errors.New("input, source-id and output are required")
	}
	b, e := readBounded(*input)
	if e != nil {
		return errors.New("cannot read snapshot")
	}
	keys := map[string][]string{}
	if *keyfile != "" {
		k, e := readBounded(*keyfile)
		if e != nil {
			return e
		}
		if e = decode(k, &keys); e != nil {
			return errors.New("invalid keywords JSON")
		}
	}
	entries, m, e := transform(b, *source, *marker, keys, time.Now().UTC())
	if e != nil {
		return e
	}
	m, e = publish(*out, entries, m, *empty)
	if e != nil {
		return e
	}
	return json.NewEncoder(w).Encode(m)
}
func main() {
	if e := run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "knowledge sync failed:", e)
		os.Exit(1)
	}
}
