package odoo

import (
	"strings"
	"testing"
)

func TestValidateConformanceRecord_Valid(t *testing.T) {
	record := ConformanceRecord{
		OperationID:     "identity.contact.create",
		ProviderVersion: "19.0-20260803",
		ImageDigest:     "sha256:0bfcfcdd2e1c80f499c25473b1b9ef2c1d31ad9e6d96534771b06b191ee4b89e",
		Modules:         []string{"base"},
		Status:          string(ReadinessCurrentVerified),
		EvidenceRef:     "runs/odoo-cli-conformance-test/odoo19-cli-conformance.json",
		VerifiedAt:      "2026-09-17T10:00:00Z",
	}
	if err := ValidateConformanceRecord(record); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
}

func TestValidateConformanceRecord_Rejections(t *testing.T) {
	base := ConformanceRecord{
		OperationID:     "identity.contact.create",
		ProviderVersion: "19.0-20260803",
		ImageDigest:     "sha256:0bfcfcdd2e1c80f499c25473b1b9ef2c1d31ad9e6d96534771b06b191ee4b89e",
		Modules:         []string{"base"},
		Status:          string(ReadinessCurrentVerified),
		EvidenceRef:     "runs/evidence.json",
		VerifiedAt:      "2026-09-17T10:00:00Z",
	}

	tests := []struct {
		name    string
		mutate  func(*ConformanceRecord)
		wantErr string
	}{
		{
			name:    "empty operation id",
			mutate:  func(r *ConformanceRecord) { r.OperationID = "" },
			wantErr: "has no catalog operation id",
		},
		{
			name:    "unknown operation id",
			mutate:  func(r *ConformanceRecord) { r.OperationID = "unknown.foo.bar" },
			wantErr: "unknown catalog operation",
		},
		{
			name:    "invalid provider version",
			mutate:  func(r *ConformanceRecord) { r.ProviderVersion = "18.0" },
			wantErr: "invalid provider version",
		},
		{
			name:    "invalid image digest",
			mutate:  func(r *ConformanceRecord) { r.ImageDigest = "not-a-digest" },
			wantErr: "invalid image digest",
		},
		{
			name:    "empty modules",
			mutate:  func(r *ConformanceRecord) { r.Modules = nil },
			wantErr: "no module metadata",
		},
		{
			name:    "duplicate module",
			mutate:  func(r *ConformanceRecord) { r.Modules = []string{"base", "base"} },
			wantErr: "duplicates module",
		},
		{
			name:    "untrimmed module",
			mutate:  func(r *ConformanceRecord) { r.Modules = []string{" base "} },
			wantErr: "invalid module name",
		},
		{
			name:    "non-current status",
			mutate:  func(r *ConformanceRecord) { r.Status = string(ReadinessHistoricalOnly) },
			wantErr: "non-current status",
		},
		{
			name:    "absolute evidence ref",
			mutate:  func(r *ConformanceRecord) { r.EvidenceRef = "/tmp/evidence.json" },
			wantErr: "unsafe evidence reference",
		},
		{
			name:    "traversal evidence ref",
			mutate:  func(r *ConformanceRecord) { r.EvidenceRef = "../evidence.json" },
			wantErr: "unsafe evidence reference",
		},
		{
			name:    "url scheme evidence ref",
			mutate:  func(r *ConformanceRecord) { r.EvidenceRef = "http://example.com/evidence.json" },
			wantErr: "unsafe evidence reference",
		},
		{
			name:    "invalid verifiedAt",
			mutate:  func(r *ConformanceRecord) { r.VerifiedAt = "2026/09/17" },
			wantErr: "invalid verification time",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			err := ValidateConformanceRecord(r)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNewCandidateConformanceRecord(t *testing.T) {
	record, err := NewCandidateConformanceRecord(
		"identity.contact.create",
		"19.0-20260803",
		"sha256:0bfcfcdd2e1c80f499c25473b1b9ef2c1d31ad9e6d96534771b06b191ee4b89e",
		[]string{"base"},
		"runs/evidence.json",
		"2026-09-17T10:00:00Z",
	)
	if err != nil {
		t.Fatalf("NewCandidateConformanceRecord failed: %v", err)
	}
	if record.Status != string(ReadinessCurrentVerified) {
		t.Fatalf("record status = %q, want %q", record.Status, ReadinessCurrentVerified)
	}
	if len(record.Modules) != 1 || record.Modules[0] != "base" {
		t.Fatalf("record modules = %v, want [base]", record.Modules)
	}

	// Should reject invalid
	_, err = NewCandidateConformanceRecord(
		"not.an.operation",
		"19.0",
		"sha256:0bfcfcdd2e1c80f499c25473b1b9ef2c1d31ad9e6d96534771b06b191ee4b89e",
		[]string{"base"},
		"runs/evidence.json",
		"2026-09-17T10:00:00Z",
	)
	if err == nil {
		t.Fatal("expected error for unknown operation")
	}
}

func TestValidateConformanceRecord_SafeReads(t *testing.T) {
	digest := "sha256:0bfcfcdd2e1c80f499c25473b1b9ef2c1d31ad9e6d96534771b06b191ee4b89e"
	evidenceRef := "runs/odoo-cli-conformance-test/odoo19-cli-conformance.json"
	verifiedAt := "2026-09-17T10:00:00Z"

	for _, id := range SafeConformanceReadIDs() {
		op, ok := CatalogByID[id]
		if !ok {
			t.Fatalf("operation %s not found in CatalogByID", id)
		}
		record, err := NewCandidateConformanceRecord(
			id,
			"19.0-20260803",
			digest,
			op.RequiredModules,
			evidenceRef,
			verifiedAt,
		)
		if err != nil {
			t.Fatalf("NewCandidateConformanceRecord for %s failed: %v", id, err)
		}
		if err := ValidateConformanceRecord(record); err != nil {
			t.Fatalf("ValidateConformanceRecord for %s rejected: %v", id, err)
		}
	}
}
