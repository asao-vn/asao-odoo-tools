package review

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// The risk-pattern catalog (patterns.json): patterns of error, risk and fraud in an Odoo business, each
// mapped to the Odoo fields that show it, its place in the ACFE Fraud Tree and its sources. A pattern whose
// status is built is one of this package's checks; the others are what the checks do not cover yet.
// scripts/dev/odoo-risk-patterns.py places every field on the catalog's read operations and writes the
// coverage report (docs/architecture/provider-research/odoo-review/risk-patterns.md).
//
//go:embed patterns.json
var patternsJSON []byte

// Pattern is one entry of the risk-pattern catalog.
type Pattern struct {
	ID         string             `json:"id"`
	Process    string             `json:"process"`
	Title      map[string]string  `json:"title"`
	Kind       string             `json:"kind"`
	Severity   string             `json:"severity"`
	Scheme     *string            `json:"scheme"`
	Signal     string             `json:"signal"`
	Formula    string             `json:"formula"`
	Fields     []string           `json:"fields"`
	Status     string             `json:"status"`
	Check      string             `json:"check,omitempty"`
	Operations []string           `json:"operations,omitempty"`
	Params     map[string]float64 `json:"params,omitempty"`
	Priority   int                `json:"priority"`
	Causes     []string           `json:"causes"`
	Sources    []string           `json:"sources"`
	Cases      []string           `json:"cases"`
	Notes      string             `json:"notes,omitempty"`
}

// PatternSource is where a pattern's knowledge comes from, and on what terms it is used.
type PatternSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	License string `json:"license"`
	Note    string `json:"note"`
}

// PatternCatalog is the whole of patterns.json.
type PatternCatalog struct {
	SchemaVersion  string                   `json:"schemaVersion"`
	About          string                   `json:"about"`
	Kinds          map[string]string        `json:"kinds"`
	Statuses       map[string]string        `json:"statuses"`
	Causes         []string                 `json:"causes"`
	DesignedChecks map[string]string        `json:"designedChecks"`
	Sources        map[string]PatternSource `json:"sources"`
	Patterns       []Pattern                `json:"patterns"`
}

var (
	patternsOnce sync.Once
	patterns     PatternCatalog
	patternsErr  error
)

// Patterns answers the risk-pattern catalog. The embedded file is checked by the package's tests, so an
// error here means a build from a broken tree.
func Patterns() (PatternCatalog, error) {
	patternsOnce.Do(func() { patternsErr = json.Unmarshal(patternsJSON, &patterns) })
	return patterns, patternsErr
}
