package odoo

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sort"
	"sync"
)

// ComputeCatalogIdentity hashes the sorted unique SemanticID set and answers
// 'what SemanticIDs does this runtime contain'. It cannot detect a change in
// shared execution BEHAVIOUR — a service built before a strategies.go fix
// reports the same catalog identity as fresh source, so a staleness check
// passes while the binary behaves differently. This identity answers the
// orthogonal question: 'was this binary built from the execution behaviour the
// caller expects'. The two must never be conflated.

// executionSources embeds the source files comprising shared execution behaviour.
//
// odoo19_catalog.go is deliberately excluded: catalog membership is the catalog
// identity's job, and the two identities are kept orthogonal.
// In addition, *_test.go and the generated catalog (odoo19_surface_catalog.gen.go) are excluded
// because they are not shared execution behaviour. provider_series.go (the series gate),
// provider_dialect.go (what a series is translated into) and odoo20_dialect_renames.gen.go (the
// renames that dialect applies, generated from the series' inventories) are execution behaviour:
// a runtime with an older dialect must not pass as current. So is provider_dialog.go (which
// provider dialogs end an operation and how one is completed), and so are provider_fit.go and
// odoo_standard_modules.json (what the provider-fit check refuses, and the stock module list it
// compares with).
//
// direct_cli.go is not part of this package: the standalone Wave 2 CLI was not admitted with
// the semantic core, so it cannot contribute to shared execution behaviour here.
//
//go:embed adapter.go catalog_identity.go execution.go normalize.go odoo19_contract_types.go odoo19_semantic_error.go odoo20_dialect_renames.gen.go odoo_standard_modules.json provider_dialect.go provider_dialog.go provider_error.go provider_fit.go provider_series.go relations.go rpc.go rpc_json2.go strategies.go surface_catalog.go transport.go
var executionSources embed.FS

var (
	executionBuildIdentityOnce   sync.Once
	cachedExecutionBuildIdentity string
)

// ExecutionBuildIdentity returns a sha256 hex digest representing the build identity
// of the shared execution behaviour.
//
// Algorithm:
//   - read every embedded file, sort the names ascending byte order
//   - sha256 over, for each file: name bytes, a 0x00 byte, file bytes, a 0x00 byte
//   - return lowercase hex
//   - compute it once with sync.Once and cache it; the embedded content cannot change at runtime
//   - hash CONTENT only: no timestamps, no absolute paths, no build metadata
func ExecutionBuildIdentity() string {
	executionBuildIdentityOnce.Do(func() {
		entries, err := fs.ReadDir(executionSources, ".")
		if err != nil {
			panic("odoo: failed to read embedded execution sources: " + err.Error())
		}

		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)

		h := sha256.New()
		for _, name := range names {
			data, err := executionSources.ReadFile(name)
			if err != nil {
				panic("odoo: failed to read embedded execution source " + name + ": " + err.Error())
			}
			h.Write([]byte(name))
			h.Write([]byte{0x00})
			h.Write(data)
			h.Write([]byte{0x00})
		}
		cachedExecutionBuildIdentity = hex.EncodeToString(h.Sum(nil))
	})
	return cachedExecutionBuildIdentity
}
