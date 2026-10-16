package upgradecheck

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
)

// The maps this build ships, one per pair of series: data/<from>-<to>.json.gz, made by
// scripts/dev/odoo-upgrade-check/bundle.py from the registries of the two conformance labs.
//
//go:embed data/*.json.gz
var data embed.FS

// SchemaVersion is the version of the map's format.
const SchemaVersion = "odoo.upgrade-map/v1"

// Lab is one of the two conformance labs a map was made from.
type Lab struct {
	ServerVersion string            `json:"serverVersion"`
	Image         string            `json:"image"`
	Modules       map[string]string `json:"modules"`
}

// Change is one entry of a map: a model or field of the source lab that the target lab lost,
// renamed or changed.
type Change struct {
	Change string `json:"change"`
	Model  string `json:"model"`
	Field  string `json:"field,omitempty"`
	// To is the new name of a FIELD_RENAMED, and the new [type, relation] of a FIELD_TYPE_CHANGED.
	To         json.RawMessage `json:"to,omitempty"`
	Values     []string        `json:"values,omitempty"`
	Candidates []string        `json:"candidates,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Evidence   string          `json:"evidence,omitempty"`
}

type targetModel struct {
	Fields map[string]Field `json:"fields"`
}

// Pair is the map of one upgrade, from one series to the next.
type Pair struct {
	SchemaVersion      string                 `json:"schemaVersion"`
	From               string                 `json:"from"`
	To                 string                 `json:"to"`
	SourceLab          Lab                    `json:"sourceLab"`
	TargetLab          Lab                    `json:"targetLab"`
	EnterpriseModules  []string               `json:"enterpriseModules"`
	ModulesNotOnTarget []string               `json:"modulesNotOnTarget"`
	Target             map[string]targetModel `json:"target"`
	Changes            []Change               `json:"changes"`

	renamed       map[[2]string]string
	removedFields map[[2]string]Change
	removedModels map[string]bool
	dropped       map[[2]string]map[string]bool
	unsearchable  map[[2]string]bool
	unstored      map[[2]string]bool
	labGaps       map[[2]string]bool
	enterprise    map[string]bool
}

// PairInfo describes a map without its data.
type PairInfo struct {
	From          string         `json:"from"`
	To            string         `json:"to"`
	SourceVersion string         `json:"sourceVersion"`
	SourceImage   string         `json:"sourceImage"`
	TargetVersion string         `json:"targetVersion"`
	TargetImage   string         `json:"targetImage"`
	Installed     map[string]int `json:"installedModules"`
	Changes       map[string]int `json:"changes"`
}

var (
	pairsMu sync.Mutex
	pairs   = map[string]*Pair{}
)

// Load returns the map from one series to the next, or an error naming the maps this build has.
func Load(from, to string) (*Pair, error) {
	pairsMu.Lock()
	defer pairsMu.Unlock()
	key := from + "-" + to
	if pair, ok := pairs[key]; ok {
		return pair, nil
	}
	compressed, err := data.ReadFile("data/" + key + ".json.gz")
	if err != nil {
		return nil, fmt.Errorf("upgradecheck: no map from %s to %s (this build has %s)", from, to, strings.Join(Available(), ", "))
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("upgradecheck: map %s: %w", key, err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("upgradecheck: map %s: %w", key, err)
	}
	pair := &Pair{}
	if err := json.Unmarshal(raw, pair); err != nil {
		return nil, fmt.Errorf("upgradecheck: map %s: %w", key, err)
	}
	if pair.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("upgradecheck: map %s has schema %q, not %q", key, pair.SchemaVersion, SchemaVersion)
	}
	pair.index()
	pairs[key] = pair
	return pair, nil
}

// Available names the maps this build has, as "19.0 -> 20.0".
func Available() []string {
	entries, _ := data.ReadDir("data")
	var out []string
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".json.gz")
		if from, to, ok := strings.Cut(name, "-"); ok {
			out = append(out, from+" -> "+to)
		}
	}
	sort.Strings(out)
	return out
}

// Pairs describes every map this build has.
func Pairs() ([]PairInfo, error) {
	entries, err := data.ReadDir("data")
	if err != nil {
		return nil, err
	}
	var out []PairInfo
	for _, entry := range entries {
		name := strings.TrimSuffix(path.Base(entry.Name()), ".json.gz")
		from, to, ok := strings.Cut(name, "-")
		if !ok {
			continue
		}
		pair, err := Load(from, to)
		if err != nil {
			return nil, err
		}
		out = append(out, pair.Info())
	}
	return out, nil
}

// Info describes the map.
func (p *Pair) Info() PairInfo {
	installed := func(lab Lab) int {
		count := 0
		for _, state := range lab.Modules {
			if state == "installed" {
				count++
			}
		}
		return count
	}
	changes := map[string]int{}
	for _, change := range p.Changes {
		changes[change.Change]++
	}
	return PairInfo{From: p.From, To: p.To, SourceVersion: p.SourceLab.ServerVersion, SourceImage: p.SourceLab.Image,
		TargetVersion: p.TargetLab.ServerVersion, TargetImage: p.TargetLab.Image,
		Installed: map[string]int{p.From: installed(p.SourceLab), p.To: installed(p.TargetLab)}, Changes: changes}
}

// Enterprise reports whether module is one of the source series' Enterprise modules, which the
// Community labs behind a map never install.
func (p *Pair) Enterprise(module string) bool { return p.enterprise[module] }

func (p *Pair) index() {
	p.renamed = map[[2]string]string{}
	p.removedFields = map[[2]string]Change{}
	p.removedModels = map[string]bool{}
	p.dropped = map[[2]string]map[string]bool{}
	p.unsearchable = map[[2]string]bool{}
	p.unstored = map[[2]string]bool{}
	p.labGaps = map[[2]string]bool{}
	p.enterprise = map[string]bool{}
	for _, module := range p.EnterpriseModules {
		p.enterprise[module] = true
	}
	for _, change := range p.Changes {
		key := [2]string{change.Model, change.Field}
		switch change.Change {
		case "FIELD_RENAMED":
			var to string
			if json.Unmarshal(change.To, &to) == nil && to != "" {
				p.renamed[key] = to
			}
		case "FIELD_REMOVED":
			p.removedFields[key] = change
		case "MODEL_REMOVED":
			p.removedModels[change.Model] = true
		case "SELECTION_VALUES_REMOVED":
			values := map[string]bool{}
			for _, value := range change.Values {
				values[value] = true
			}
			p.dropped[key] = values
		case "FIELD_NO_LONGER_SEARCHABLE":
			p.unsearchable[key] = true
		case "FIELD_NO_LONGER_STORED":
			p.unstored[key] = true
		case "FIELD_NOT_IN_TARGET_LAB", "MODEL_NOT_IN_TARGET_LAB":
			p.labGaps[key] = true
		}
	}
}
