package odoo

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// CatalogIdentity carries the operation count and the content hash of the catalog.
// The hash changes when catalog MEMBERSHIP changes even at constant count;
// OperationCount is carried for diagnosability only and is NOT the identity.
type CatalogIdentity struct {
	OperationCount      int    `json:"operation_count"`
	SemanticIDSetSHA256 string `json:"semantic_id_set_sha256"`
}

// ComputeCatalogIdentity calculates the identity of the current semantic catalog.
//
// Recipe for SemanticIDSetSHA256 (document it in a comment verbatim — organization-service and the
// conformance harness both depend on it byte-for-byte):
//  1. collect every SemanticID from the catalog
//  2. de-duplicate
//  3. sort ascending, byte order
//  4. join with "\n" and append one trailing "\n"
//  5. sha256 of those bytes, lowercase hex
//
// Comment must state: the hash changes when catalog MEMBERSHIP changes even at constant count;
// OperationCount is carried for diagnosability only and is NOT the identity.
func ComputeCatalogIdentity() CatalogIdentity {
	ops := Catalog()
	if len(ops) == 0 {
		return CatalogIdentity{
			OperationCount:      0,
			SemanticIDSetSHA256: "",
		}
	}
	ids := make([]string, len(ops))
	for i, op := range ops {
		ids[i] = op.SemanticID
	}
	return CatalogIdentity{
		OperationCount:      len(ops),
		SemanticIDSetSHA256: ComputeSemanticIDSetSHA256(ids),
	}
}

// ComputeSemanticIDSetSHA256 hashes a set of semantic IDs according to the canonical recipe:
//  1. collect every SemanticID from the catalog
//  2. de-duplicate
//  3. sort ascending, byte order
//  4. join with "\n" and append one trailing "\n"
//  5. sha256 of those bytes, lowercase hex
//
// The hash changes when catalog MEMBERSHIP changes even at constant count;
// OperationCount is carried for diagnosability only and is NOT the identity.
//
// If the input slice is empty or contains no valid non-empty IDs, an empty string is returned
// to yield a well-defined result the caller can detect as unusable (no silent success).
func ComputeSemanticIDSetSHA256(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return ""
	}
	sort.Strings(unique)
	joined := strings.Join(unique, "\n") + "\n"
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])
}
