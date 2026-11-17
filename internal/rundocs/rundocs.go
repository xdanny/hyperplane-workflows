// Package rundocs reads the documents an SDK run actually produced.
//
// Two readers of a run judged it without ever seeing what it wrote. The persona
// reviewer reported "the full note is unavailable to assess" and cited "(this
// run produced no artifact files…)"; the skill extractor's own rationale said
// "no delivered summary is shown" (S-15, D-14). Both were looking in the
// artifacts folder, which a node workflow never writes to: a step writes to
// <runDir>/nodes/delivery/OUTPUTS, and the committed checkpoint lists what it
// wrote there, with paths and sha256.
//
// So the lookup lives here once, with one set of bounds and one caption format,
// rather than twice with two. Everything is bounded and says when a bound bit —
// a reader shown a truncated document as if it were whole is being lied to.
package rundocs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

const (
	// MaxBytes bounds one document, MaxTotalBytes the set, and MaxDocuments how
	// many are read at all. A chatty run must not crowd out the instructions it
	// is being read against.
	MaxBytes      = 32 * 1024
	MaxTotalBytes = 128 * 1024
	MaxDocuments  = 12
)

// Document is one file a run's step recorded, with its text.
type Document struct {
	StepID    string
	StepTitle string
	// Path is relative to the run's outputs folder, exactly as the checkpoint
	// recorded it.
	Path string
	Text string
	// Truncated says the file is longer than MaxBytes (or than what was left of
	// the total budget), so the end is not shown.
	Truncated bool
}

// Caption names a document the way both readers head it: "<step title> · <path>".
func (d Document) Caption() string {
	if strings.TrimSpace(d.StepTitle) == "" {
		return d.Path
	}
	return d.StepTitle + " · " + d.Path
}

// OutputsDir is where an SDK run's steps write their files.
func OutputsDir(runDir string) string {
	return filepath.Join(runDir, "nodes", "delivery", "OUTPUTS")
}

// Read returns the documents the run's committed checkpoint recorded, in
// declared-step order, bounded per document and in total.
//
// The checkpoint is the authority on WHICH files count — it lists each step's
// `document` and `files` — so this never walks the folder guessing. A file the
// checkpoint names but the folder no longer holds is simply absent: a cleaned-up
// run makes the evidence thinner, never wrong.
func Read(runDir string, run store.Run) []Document {
	if runDir == "" {
		return nil
	}
	var launch struct {
		Runtime string `json:"runtime"`
		Request struct {
			Nodes []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"nodes"`
		} `json:"request"`
	}
	if json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" {
		return nil
	}
	titles := map[string]string{}
	order := map[string]int{}
	for i, node := range launch.Request.Nodes {
		titles[node.ID], order[node.ID] = node.Title, i
	}
	type recordedFile struct {
		Path string `json:"path"`
	}
	var doc struct {
		SDK struct {
			Nodes []struct {
				ID       string         `json:"id"`
				Document *recordedFile  `json:"document"`
				Files    []recordedFile `json:"files"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	if len(run.Doc) == 0 || json.Unmarshal(run.Doc, &doc) != nil {
		return nil
	}
	nodes := doc.SDK.Nodes
	sort.SliceStable(nodes, func(i, j int) bool {
		a, aok := order[nodes[i].ID]
		b, bok := order[nodes[j].ID]
		if aok != bok {
			return aok
		}
		return aok && bok && a < b
	})

	base := OutputsDir(runDir)
	var out []Document
	budget := MaxTotalBytes
	seen := map[string]bool{}
	for _, node := range nodes {
		paths := []string{}
		if node.Document != nil {
			paths = append(paths, node.Document.Path)
		}
		for _, file := range node.Files {
			paths = append(paths, file.Path)
		}
		for _, path := range paths {
			if len(out) >= MaxDocuments || budget <= 0 {
				return out
			}
			if !safeRelPath(path) || seen[path] {
				continue
			}
			seen[path] = true
			limit := MaxBytes
			if budget < limit {
				limit = budget
			}
			text, truncated, ok := readHead(filepath.Join(base, filepath.FromSlash(path)), limit)
			if !ok {
				continue
			}
			budget -= len(text)
			out = append(out, Document{
				StepID: node.ID, StepTitle: titles[node.ID], Path: path,
				Text: text, Truncated: truncated,
			})
		}
	}
	return out
}

// Paths names the documents that were read, for a one-line "Read: …" header.
func Paths(docs []Document) []string {
	out := make([]string, 0, len(docs))
	for _, doc := range docs {
		out = append(out, doc.Path)
	}
	return out
}

// safeRelPath accepts a checkpoint-recorded path that stays inside the run's own
// outputs folder. Slashes are allowed — a step may write build/totals.csv — but
// nothing that could climb out of it is.
func safeRelPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || filepath.IsAbs(p) {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// readHead returns the first n bytes of a file, cut to a rune boundary, and
// whether the file was longer. ok is false when the file cannot be read or holds
// no text — an empty document is nothing for a reader to read.
func readHead(path string, n int) (string, bool, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return "", false, false
	}
	truncated := len(b) > n
	if truncated {
		b = b[:n]
		for len(b) > 0 && b[len(b)-1]&0xC0 == 0x80 {
			b = b[:len(b)-1]
		}
		if len(b) > 0 && b[len(b)-1]&0x80 != 0 {
			b = b[:len(b)-1]
		}
	}
	text := strings.TrimSpace(strings.ToValidUTF8(string(b), ""))
	if text == "" {
		return "", false, false
	}
	return text, truncated, true
}
