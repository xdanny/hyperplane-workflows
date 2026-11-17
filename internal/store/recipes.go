// Template input definitions and substitution shared by SDK authoring paths.
package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Slot value types. `text` is the default and the only one that existed before:
// a free-text answer the process reads as prose.
//
//	SlotTypeText   — prose. The default; an empty Type means this.
//	SlotTypeList   — a list of short values (columns, ids). The form renders it
//	                 as tags/CSV; the value reaches the process as a
//	                 comma-separated string, so an instruction written against
//	                 the old free-text slot keeps working unchanged.
//	SlotTypeDate   — an ISO date (YYYY-MM-DD).
//	SlotTypeNumber — a single number.
//	SlotTypeTable  — ROWS with a declared column schema. Stored as JSON, and
//	                 substituted into the DAG as one readable line per row (see
//	                 SlotTableText), so an instruction written against the old
//	                 free-text slot keeps reading a string.
const (
	SlotTypeText   = "text"
	SlotTypeList   = "list"
	SlotTypeDate   = "date"
	SlotTypeNumber = "number"
	SlotTypeTable  = "table"
)

// Slot audiences: WHO can answer this slot. Priya's finding was that half the
// required slots on a data recipe are an engineer's questions with the owner's
// name on them, and the form gave no way to tell which was which.
//
//	SlotAudienceOwner    — the person who wants the work done. The default.
//	SlotAudienceEngineer — whoever runs their data: table names, grain, keys,
//	                       where it executes.
const (
	SlotAudienceOwner    = "owner"
	SlotAudienceEngineer = "engineer"
)

// ContextSlot is one typed thing a recipe must know before it can start.
// The id is the substitution key ({slot:<id>}) and the run-input name.
type ContextSlot struct {
	ID    string `json:"id"`
	Label string `json:"label"`

	// Hint is read by THE PERSON FILLING THE SLOT, not by the model that
	// consumes the value. The authoring contract, in full:
	//
	//	Name why the slot matters to them, define every term in the sentence
	//	that uses it, and give the default when one exists. The model-facing
	//	description lives in the process contract (contract.inputs[].description)
	//	and is NEVER copied here.
	//
	// The exemplar is the pipeline-change recipe: every one of its hints names
	// its audience, says why the slot matters, and defines its term of art in
	// the sentence that uses it. A hint that reuses the contract sentence is the
	// defect this contract exists to stop — it is how "restated", "leakage" and
	// "analytical models" reached a form with no definition anywhere near them.
	//
	// The same contract governs the OUTPUT side, which had the identical defect
	// one field over:
	//
	//	An output's person-facing line names the thing in the reader's words
	//	and says what it lets them do; the model-facing description stays in
	//	the contract and is never rendered.
	//
	// That line is authored on process.Field.ForOwner, and the recipe form's
	// "What you will get back" renders it and nothing else.
	//
	// And it governs the project-declaration form's GROUP and FIELD hints, in
	// apps/web/src/components/DeclarationGroups.tsx: same rule, same reason —
	// a hint written for whoever built the registry ("no workspace-write node
	// may read it", "travels into every brief verbatim") is a hint written for
	// the wrong reader.
	Hint     string `json:"hint,omitempty"`
	Required bool   `json:"required,omitempty"`
	Example  string `json:"example,omitempty"`

	// Type is the value shape: text (default) | list | date | number | table.
	// It changes how the form renders the field and how the value is normalized
	// — never how the process reads it, which is always a string.
	Type string `json:"type,omitempty"`

	// Columns is the row schema of a `table` slot, in display order. Required
	// for a table and meaningless for anything else: a table with no columns is
	// a textarea wearing a grid, which is the shape this type exists to end.
	Columns []SlotColumn `json:"columns,omitempty"`

	// Audience is who can answer: owner (default) | engineer. The form groups
	// slots under "From you" and "From whoever runs your data" so an owner can
	// see at a glance which half they are blocked on, and hand the other half
	// on as a list rather than guessing at it.
	Audience string `json:"audience,omitempty"`
}

// SlotType is a slot's declared value type, defaulting to text.
func (s ContextSlot) SlotType() string {
	if s.Type == "" {
		return SlotTypeText
	}
	return s.Type
}

// SlotAudience is who a slot is addressed to, defaulting to the owner.
func (s ContextSlot) SlotAudience() string {
	if s.Audience == "" {
		return SlotAudienceOwner
	}
	return s.Audience
}

// NormalizeSlotValue canonicalizes a raw slot value for its declared type.
//
// A `list` slot is stored and passed on as a COMMA-SEPARATED STRING: the form
// may collect it as tags, newlines or a pasted CSV, and every one of those
// reaches the process as "a, b, c". That is deliberate — the process
// instructions and the inputs gates were written against a free-text value, and
// typing the slot must not force a rewrite of either.
//
// The literal `none` is preserved as a list member rather than treated as an
// empty value: "none, VendorID, pickup_time" is the owner declaring that this
// data has NO natural key and naming the closest candidate, which is a real
// answer and not a blank.
func NormalizeSlotValue(slot ContextSlot, raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	switch slot.SlotType() {
	case SlotTypeList:
		parts := strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ';'
		})
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, ", ")
	case SlotTypeTable:
		// Canonical JSON, whichever of the two renderings arrived: a table that
		// came back from a run carries the text form, and a table typed into the
		// form carries JSON. Both are the same rows.
		return SlotTableJSON(slot, SlotTableRows(slot, v))
	default:
		return v
	}
}

// SlotListValues splits a list slot's stored value back into its members.
// Returns nil for a non-list slot or an empty value.
func SlotListValues(slot ContextSlot, raw string) []string {
	if slot.SlotType() != SlotTypeList {
		return nil
	}
	norm := NormalizeSlotValue(slot, raw)
	if norm == "" {
		return nil
	}
	return strings.Split(norm, ", ")
}

// ── Table slots ─────────────────────────────────────────────────────────────
//
// Three personas independently reported the same defect from three directions:
// one prose box carrying N features' tolerances, so one tolerance gets borrowed
// across features and the human approves the borrowing without seeing it. The
// fix is not more words in the hint — it is a row per feature, with the unit as
// a column rather than a hope.
//
// A table slot has TWO renderings and they are not interchangeable:
//
//	stored  — canonical JSON, `[{"feature":"trip_distance","unit":"psi"},…]`.
//	          This is what the form edits, what a project prefill produces, and
//	          what a gate that wants a field reads.
//	carried — one readable line per row, `1. feature: trip_distance | unit: psi`.
//	          This is what is substituted into the DAG and what arrives in
//	          $HYPERPLANE_INPUT_<SLOT>, so every instruction and every gate
//	          written against the old free-text slot keeps working unchanged.
//
// The carried form is parseable back into rows (ParseSlotTableText), which is
// what lets a POSIX-sh gate check "every categorical feature carries a
// tolerance" without a JSON parser, and what the round-trip test pins.

// SlotColumn is one column of a table slot: what to ask for in that cell.
type SlotColumn struct {
	// ID is the key in the stored JSON and in the carried line. Stable and
	// machine-read: gates match on it.
	ID string `json:"id"`
	// Label is what the form's column header says.
	Label string `json:"label,omitempty"`
	// Hint is the one-line help under the header, held to the same authoring
	// contract as ContextSlot.Hint.
	Hint string `json:"hint,omitempty"`
	// Options closes the column to a set — a unit column with eight units is
	// the difference between "0.5%" and a number the platform understands.
	Options []string `json:"options,omitempty"`
	// Required marks a cell every row must fill. Enforced by the recipe's own
	// inputs gate, not here: the store records the answer, the gate refuses the
	// run.
	Required bool `json:"required,omitempty"`
}

// slotTableFieldSep separates the fields of one carried row, and slotTableKV
// separates a field's key from its value. Both are chosen to survive prose: a
// "why it matters" cell may contain commas, semicolons and colons, and only a
// pipe is replaced on the way in.
const (
	slotTableFieldSep = " | "
	slotTableKV       = ": "
)

// SlotTableRows decodes a table slot's stored value into rows keyed by column
// id. Unknown keys are dropped and declared columns that a row omits are simply
// absent — a table slot never invents a cell.
//
// Accepts the CARRIED form as well as the stored JSON, so a value that has been
// through a DAG and back (a repair, a re-instantiation from a run's inputs) is
// still readable as rows.
func SlotTableRows(slot ContextSlot, raw string) []map[string]string {
	if slot.SlotType() != SlotTypeTable {
		return nil
	}
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "[") {
		var rows []map[string]any
		if err := json.Unmarshal([]byte(v), &rows); err != nil {
			return nil
		}
		known := map[string]bool{}
		for _, c := range slot.Columns {
			known[c.ID] = true
		}
		out := make([]map[string]string, 0, len(rows))
		for _, r := range rows {
			row := map[string]string{}
			for k, val := range r {
				if len(known) > 0 && !known[k] {
					continue
				}
				if s := cellString(val); s != "" {
					row[k] = s
				}
			}
			if len(row) > 0 {
				out = append(out, row)
			}
		}
		return out
	}
	return ParseSlotTableText(slot, v)
}

// cellString renders one decoded JSON cell as the string the row carries.
// Numbers keep their exact spelling rather than becoming 1.2000000000000002.
func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// jsonQuote renders one string as a JSON string WITHOUT Go's default HTML
// escaping.
//
// It matters here and nowhere else: a threshold cell reads ">= 0.65", and
// encoding/json would store it as "\u003e= 0.65" while the browser's
// JSON.stringify stores ">= 0.65". The two spellings are the same value and a
// different string — which would make an inherited table stop comparing equal
// to the project's declaration the moment it round-tripped, dropping the
// "inherited from project" tag on a value nobody had edited.
func jsonQuote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// A string is always encodable; the fallback keeps this total.
		q, _ := json.Marshal(s)
		return string(q)
	}
	return strings.TrimRight(b.String(), "\n")
}

// SlotTableJSON is the canonical stored form: rows in declaration order, cells
// in COLUMN order (not map order), blanks omitted. Canonical because the form
// compares a slot's value against the project's prefill string to decide
// whether it is still inherited, and two orderings of the same table would read
// as an edit.
func SlotTableJSON(slot ContextSlot, rows []map[string]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('[')
	written := 0
	for _, row := range rows {
		cells := slotTableCells(slot, row)
		if len(cells) == 0 {
			continue
		}
		if written > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		for i, c := range cells {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(jsonQuote(c[0]))
			b.WriteByte(':')
			b.WriteString(jsonQuote(c[1]))
		}
		b.WriteByte('}')
		written++
	}
	b.WriteByte(']')
	if written == 0 {
		return ""
	}
	return b.String()
}

// slotTableCells orders one row's non-empty cells by the slot's columns, then
// appends any extra keys in sorted order so a cell is never silently lost.
func slotTableCells(slot ContextSlot, row map[string]string) [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	for _, c := range slot.Columns {
		seen[c.ID] = true
		if v := sanitizeCell(row[c.ID]); v != "" {
			out = append(out, [2]string{c.ID, v})
		}
	}
	var extra []string
	for k := range row {
		if !seen[k] && sanitizeCell(row[k]) != "" {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, [2]string{k, sanitizeCell(row[k])})
	}
	return out
}

// sanitizeCell collapses a cell to one line and frees the field separator, so a
// pasted paragraph cannot break the carried form's parse.
func sanitizeCell(v string) string {
	v = strings.ReplaceAll(v, "|", "/")
	return strings.Join(strings.Fields(v), " ")
}

// SlotTableText is the CARRIED form: one numbered line per row, each line a
// `key: value` list separated by ` | `, blanks omitted.
//
//  1. feature: trip_distance | type: numeric | tolerance: 0.2 | unit: psi
//  2. feature: payment_type | type: categorical | tolerance: 2 | unit: points_of_share
//
// Row order is the order they were declared in, which for a metric table means
// the PRIMARY metric is line 1 — the platform's eval-gate parses the declared
// metric name and threshold out of the head of this string.
func SlotTableText(slot ContextSlot, raw string) string {
	rows := SlotTableRows(slot, raw)
	if len(rows) == 0 {
		return ""
	}
	var lines []string
	for i, row := range rows {
		cells := slotTableCells(slot, row)
		if len(cells) == 0 {
			continue
		}
		parts := make([]string, 0, len(cells))
		for _, c := range cells {
			parts = append(parts, c[0]+slotTableKV+c[1])
		}
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, strings.Join(parts, slotTableFieldSep)))
	}
	return strings.Join(lines, "\n")
}

// ParseSlotTableText reads the carried form back into rows. It is the inverse
// of SlotTableText for any table whose cells went through sanitizeCell, which
// is every table the platform writes.
func ParseSlotTableText(slot ContextSlot, text string) []map[string]string {
	var out []map[string]string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.Index(line, ". "); i > 0 && isDigits(line[:i]) {
			line = line[i+2:]
		}
		row := map[string]string{}
		for _, field := range strings.Split(line, strings.TrimSpace(slotTableFieldSep)) {
			field = strings.TrimSpace(field)
			i := strings.Index(field, ":")
			if i <= 0 {
				continue
			}
			k := strings.TrimSpace(field[:i])
			v := strings.TrimSpace(field[i+1:])
			if k != "" && v != "" {
				row[k] = v
			}
		}
		if len(row) > 0 {
			out = append(out, row)
		}
	}
	return out
}

// indentLines prefixes every line of s with pad. Used to nest a table's rows
// under their label in the CONTEXT block.
func indentLines(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SlotCarriedValue is what a slot's value looks like once it reaches the DAG:
// the carried text for a table, and the value itself for every other type. One
// function so the spec substitution, the CONTEXT block and any caller that
// wants "what the process will actually read" cannot disagree.
func SlotCarriedValue(slot ContextSlot, raw string) string {
	if slot.SlotType() == SlotTypeTable {
		return SlotTableText(slot, raw)
	}
	return strings.TrimSpace(raw)
}

// ValidateContextSlots reports slot declarations that cannot be rendered or
// read. It is called by the seed lint so a recipe with a table slot and no
// columns fails the gate rather than the form.
func ValidateContextSlots(slots []ContextSlot) []string {
	var problems []string
	seen := map[string]bool{}
	for _, s := range slots {
		if strings.TrimSpace(s.ID) == "" {
			problems = append(problems, "a slot has no id — the id is the substitution key")
			continue
		}
		if seen[s.ID] {
			problems = append(problems, fmt.Sprintf("slot %q is declared twice", s.ID))
		}
		seen[s.ID] = true
		if s.SlotType() != SlotTypeTable {
			if len(s.Columns) > 0 {
				problems = append(problems, fmt.Sprintf(
					"slot %q declares columns but is a %s slot — columns belong to a table", s.ID, s.SlotType()))
			}
			continue
		}
		if len(s.Columns) == 0 {
			problems = append(problems, fmt.Sprintf(
				"slot %q is a table with no columns — a table with no column schema is a textarea wearing a grid", s.ID))
			continue
		}
		cols := map[string]bool{}
		for _, c := range s.Columns {
			if strings.TrimSpace(c.ID) == "" {
				problems = append(problems, fmt.Sprintf("slot %q has a column with no id", s.ID))
				continue
			}
			if cols[c.ID] {
				problems = append(problems, fmt.Sprintf("slot %q declares column %q twice", s.ID, c.ID))
			}
			cols[c.ID] = true
			if strings.TrimSpace(c.Label) == "" {
				problems = append(problems, fmt.Sprintf(
					"slot %q column %q has no label — the header is what the person filling the row reads", s.ID, c.ID))
			}
		}
	}
	return problems
}

// Criterion is one thing a recipe promises to prove. Criteria are carried
// verbatim onto the run doc; they are policy, not execution.
//
// Proof names the node whose verdict settles this criterion — usually a
// verify/gate INSIDE the trusted process the recipe wraps, which is why it is a
// bare node id rather than a path: the delivery view searches the whole run
// tree. It is optional and advisory. When it is empty, or names a node that
// never ran, the criterion reads "not verified" rather than borrowing a verdict
// from somewhere else. Criteria live in jsonb, so adding it needs no migration.
type Criterion struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Hint  string `json:"hint,omitempty"`
	Proof string `json:"proof,omitempty"`
}

// MissingSlots returns the ids of required slots whose value is absent or
// blank, in declaration order. An empty result means the recipe may start.
func MissingSlots(slots []ContextSlot, values map[string]string) []string {
	var missing []string
	for _, s := range slots {
		if !s.Required {
			continue
		}
		// Normalized, not raw: an empty table arrives as "[]" and a required
		// table with no rows is exactly as unfilled as an empty textarea.
		v := NormalizeSlotValue(s, values[s.ID])
		if v == "" {
			missing = append(missing, s.ID)
			continue
		}
		// A table with rows is not a table that is filled in. An inherited
		// features row arrived with its required Type and Why cells blank, the
		// form counted the slot as answered, and the recipe's own gate refused
		// the run one screen later. A required cell is required here too.
		if len(IncompleteTableRows(s, v)) > 0 {
			missing = append(missing, s.ID)
		}
	}
	return missing
}

// IncompleteTableRows names the required cells a table slot's rows leave empty,
// one sentence per cell, in row then column order. An empty result means every
// row answers every required column.
//
// It is exported because three surfaces have to agree about what "ready" means:
// the recipe form's counter, the instantiate path that refuses the run, and the
// tests that hold them together.
func IncompleteTableRows(slot ContextSlot, raw string) []string {
	if slot.SlotType() != SlotTypeTable {
		return nil
	}
	rows := SlotTableRows(slot, raw)
	var out []string
	for i, row := range rows {
		label := ""
		for _, c := range slot.Columns {
			if v := strings.TrimSpace(row[c.ID]); v != "" {
				label = v
				break
			}
		}
		if label == "" {
			label = fmt.Sprintf("row %d", i+1)
		}
		for _, c := range slot.Columns {
			if !c.Required {
				continue
			}
			if strings.TrimSpace(row[c.ID]) != "" {
				continue
			}
			out = append(out, fmt.Sprintf("%s: row %d (%s) leaves %q empty, and it is required",
				slot.ID, i+1, label, c.Label))
		}
	}
	return out
}

// MissingSlotsError reports required context slots that were not supplied.
type MissingSlotsError struct {
	Missing []string
}

func (e *MissingSlotsError) Error() string {
	return "missing required context slot(s): " + strings.Join(e.Missing, ", ")
}

// FilterSlotValues keeps only declared slot ids that carry a non-blank value,
// trimmed. Undeclared keys are dropped — a recipe's contract is its slot list,
// not whatever the caller happened to post.
func FilterSlotValues(slots []ContextSlot, values map[string]string) map[string]string {
	out := map[string]string{}
	for _, s := range slots {
		if v := NormalizeSlotValue(s, values[s.ID]); v != "" {
			out[s.ID] = v
		}
	}
	return out
}

// FillSlots substitutes slot values into a template spec.
//
// Every {slot:<id>} token in every string of the spec is replaced with the
// supplied value. An exact unfilled binding becomes an empty value, while an
// unfilled token inside prose reads "(not declared)". Any slot that
// carries a value but is never referenced by a token is appended as a structured
// CONTEXT block to the root node's instruction, so a filled slot can never be
// silently dropped on the floor.
func FillSlots(spec JSON, slots []ContextSlot, values map[string]string) (JSON, error) {
	if len(spec) == 0 {
		spec = JSON("{}")
	}
	if len(slots) == 0 {
		return spec, nil
	}

	var doc any
	if err := json.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("fill slots: spec is not valid JSON: %w", err)
	}

	referenced := map[string]bool{}
	if root, ok := doc.(map[string]any); ok && root["kind"] == "nodes" {
		// SDK step inputs are data bindings, not human-readable prompt text.
		// Preserve canonical tables and empty optional values at this boundary.
		nodes, _ := root["nodes"].([]any)
		bindings := make([]map[string]any, len(nodes))
		for i, raw := range nodes {
			node, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			inputs, ok := node["inputs"].(map[string]any)
			if !ok {
				continue
			}
			bindings[i] = inputs
			delete(node, "inputs")
			for key, value := range inputs {
				exact := false
				for _, slot := range slots {
					if value == "{slot:"+slot.ID+"}" {
						inputs[key] = NormalizeSlotValue(slot, values[slot.ID])
						referenced[slot.ID], exact = true, true
						break
					}
				}
				if !exact {
					inputs[key] = substituteSlots(value, slots, values, referenced)
				}
			}
		}
		doc = substituteSlots(doc, slots, values, referenced)
		for i, inputs := range bindings {
			if inputs != nil {
				nodes[i].(map[string]any)["inputs"] = inputs
			}
		}
	} else {
		doc = substituteSlots(doc, slots, values, referenced)
	}

	// Slots the DAG never names still have to reach the workflow.
	var unreferenced []ContextSlot
	for _, s := range slots {
		if !referenced[s.ID] && strings.TrimSpace(values[s.ID]) != "" {
			unreferenced = append(unreferenced, s)
		}
	}
	if len(unreferenced) > 0 {
		if m, ok := doc.(map[string]any); ok {
			appendContextBlock(m, unreferenced, values)
		}
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("fill slots: %w", err)
	}
	return JSON(out), nil
}

// substituteSlots walks decoded JSON replacing {slot:<id>} inside string values,
// recording which slot ids were actually referenced.
func substituteSlots(v any, slots []ContextSlot, values map[string]string, referenced map[string]bool) any {
	switch t := v.(type) {
	case string:
		for _, s := range slots {
			token := "{slot:" + s.ID + "}"
			if !strings.Contains(t, token) {
				continue
			}
			referenced[s.ID] = true
			// The CARRIED form, never the stored one: a table reaches the DAG
			// as one readable line per row, so an instruction written against
			// the old free-text slot reads a string exactly as it always did.
			val := SlotCarriedValue(s, values[s.ID])
			if val == "" {
				if t == token {
					return ""
				}
				val = "(not declared)"
			}
			t = strings.ReplaceAll(t, token, val)
		}
		return t
	case map[string]any:
		for k, val := range t {
			t[k] = substituteSlots(val, slots, values, referenced)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = substituteSlots(val, slots, values, referenced)
		}
		return t
	default:
		return v
	}
}

// appendContextBlock appends a CONTEXT block naming the supplied slots to the
// root node's instruction. Root = the first node with no deps that carries an
// instruction; failing that, the first node with an instruction at all. Returns
// false when the spec has no instruction to carry the context (a pure
// shell/process DAG already receives the values as run inputs).
func appendContextBlock(spec map[string]any, slots []ContextSlot, values map[string]string) bool {
	nodes, ok := spec["nodes"].([]any)
	if !ok {
		return false
	}
	target := pickRootInstructionNode(nodes)
	if target == nil {
		return false
	}

	target["instruction"] = target["instruction"].(string) + contextBlock(slots, values)
	return true
}

// contextBlock renders the CONTEXT text that carries supplied values no token
// referenced. Shared by the step-level block above and by saved workflows with
// no steps, so a filled value reads the same wherever it has to land.
func contextBlock(slots []ContextSlot, values map[string]string) string {
	var b strings.Builder
	b.WriteString("\n\nCONTEXT — supplied when this recipe was instantiated:\n")
	for _, s := range slots {
		label := s.Label
		if label == "" {
			label = s.ID
		}
		val := SlotCarriedValue(s, values[s.ID])
		if s.SlotType() == SlotTypeTable {
			// A table's rows each get their own line, so the block reads as a
			// table rather than as one very long bullet.
			fmt.Fprintf(&b, "- %s:\n%s\n", label, indentLines(val, "    "))
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", label, val)
	}
	return b.String()
}

// pickRootInstructionNode returns the node map that should carry the CONTEXT
// block, or nil when no node has an instruction.
func pickRootInstructionNode(nodes []any) map[string]any {
	var fallback map[string]any
	for _, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		instr, ok := n["instruction"].(string)
		if !ok || instr == "" {
			continue
		}
		if fallback == nil {
			fallback = n
		}
		deps, _ := n["deps"].([]any)
		if len(deps) == 0 {
			return n
		}
	}
	return fallback
}

// RecipeSlotSpec is the FORM's own account of one slot, frozen onto the run.
//
// `slots` is a MAP, and a map has no order and no types: the run page rendered
// the frozen request in whatever order JSON happened to hand it back — the
// question the form asks first came out sixth — and printed a table slot as the
// raw JSON it is stored as. Neither is recoverable from the map, and the
// template it came from may have been edited since, so the answer is to freeze
// the shape ALONGSIDE the values, exactly as they stood when the run started.
//
// Deliberately thin: id, the label the form used, the value's shape, the column
// headers of a table, and who the slot was addressed to. Hints and examples are
// help for filling a form in, and this is the record of one already filled.
type RecipeSlotSpec struct {
	ID       string             `json:"id"`
	Label    string             `json:"label,omitempty"`
	Type     string             `json:"type,omitempty"`
	Audience string             `json:"audience,omitempty"`
	Columns  []RecipeSlotColumn `json:"columns,omitempty"`
}

// RecipeSlotColumn is one column header of a frozen table slot.
type RecipeSlotColumn struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// RecipeDoc builds the run-doc stamp for an instantiated recipe.
//
// The key names are a contract with the delivery view: `recipe` is the template
// id, `slots` is the filled context, `slotSpecs` is that context's shape and
// order as the form had it, and `criteria` is the recipe's criteria array
// verbatim. All four keys are always present.
//
// slotSpecs are ordered the way the FORM ordered them: everything addressed to
// the owner, then everything addressed to whoever runs their data, each in
// template order. A run's frozen request then reads in the order it was
// answered in.
func RecipeDoc(templateID string, criteria []Criterion, slots []ContextSlot, values map[string]string) (JSON, error) {
	if criteria == nil {
		criteria = []Criterion{}
	}
	if values == nil {
		values = map[string]string{}
	}
	specs := recipeSlotSpecs(slots, values)
	out, err := json.Marshal(struct {
		Recipe    string            `json:"recipe"`
		Slots     map[string]string `json:"slots"`
		SlotSpecs []RecipeSlotSpec  `json:"slotSpecs"`
		Criteria  []Criterion       `json:"criteria"`
	}{Recipe: templateID, Slots: values, SlotSpecs: specs, Criteria: criteria})
	if err != nil {
		return nil, fmt.Errorf("recipe doc: %w", err)
	}
	return JSON(out), nil
}

// recipeSlotSpecs freezes the shape of the slots that actually carry a value,
// in the form's own order. A slot with no value is left out: the request block
// shows what was asked for, and an unanswered optional slot was not.
func recipeSlotSpecs(slots []ContextSlot, values map[string]string) []RecipeSlotSpec {
	out := make([]RecipeSlotSpec, 0, len(slots))
	for _, audience := range []string{SlotAudienceOwner, SlotAudienceEngineer} {
		for _, s := range slots {
			if s.SlotAudience() != audience {
				continue
			}
			if strings.TrimSpace(values[s.ID]) == "" {
				continue
			}
			spec := RecipeSlotSpec{
				ID: s.ID, Label: strings.TrimSpace(s.Label),
				Type: s.SlotType(), Audience: audience,
			}
			for _, c := range s.Columns {
				spec.Columns = append(spec.Columns, RecipeSlotColumn{ID: c.ID, Label: c.Label})
			}
			out = append(out, spec)
		}
	}
	return out
}
