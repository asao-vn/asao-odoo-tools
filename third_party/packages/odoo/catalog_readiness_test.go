package odoo

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestReadinessCoversTheCanonicalCatalog(t *testing.T) {
	report := CurrentConformanceReport()
	// 73 reviewed records of 2026-09-17, plus the four Gate 2 retail reads promoted on 2026-10-10 by
	// the oracle-backed read conformance run (scripts/dev/odoo-read-conformance.py), plus the money
	// flows of 2026-10-10: 9 actions and wizards that agreed with the web client's path on fresh twins
	// (scripts/dev/odoo-action-conformance.py) and 10 historical lists that agreed with the oracle, plus
	// 4 historical describes (product, invoice, contact, employee) on 2026-10-11, once the read oracle
	// read a curated answer back through the names, enum spellings and relation shape describe publishes,
	// and transfer reservation and the two guarded deletes, on fresh twins the same day.
	if report.CatalogOperations != 105 || report.CurrentVerified != 103 || report.HistoricalOnly != 2 || report.Unverified != 0 {
		t.Fatalf("readiness report = %+v", report)
	}
	// The manifest also holds the surface operations the read, write and action runs promoted (291
	// reads, 219 creates/updates, 70 actions and wizards); they never count above. 2026-10-11 added
	// 40 reads and 15 writes (attachments, journal entries, counts, settings, system parameters,
	// website media and layouts, pricelist items, payments) and 24 actions (notes and activities on
	// ten kinds of record, posting and resetting journal entries, applying and clearing counts) and the
	// 7 prints (scripts/dev/odoo-report-conformance.py), and 6 guarded deletes. Then 7 list-view actions
	// (work order start/pause/finish, unreserve, reorder rules' replenish/automate/clear), on fresh twins.
	if len(report.Records) != 103+291+219+77+7+6 {
		t.Fatalf("current manifest has %d records, want 103 curated + 600 surface", len(report.Records))
	}

	counts := map[OperationReadiness]int{}
	for _, operation := range Catalog() {
		readiness, ok := ReadinessOf(operation.SemanticID)
		if !ok {
			t.Fatalf("catalog operation %q has no readiness", operation.SemanticID)
		}
		counts[readiness]++
	}
	if counts[ReadinessCurrentVerified] != 103 || counts[ReadinessHistoricalOnly] != 2 || counts[ReadinessUnverified] != 0 {
		t.Fatalf("readiness counts = %v", counts)
	}
	if _, ok := ReadinessOf("not.a.catalog.operation"); ok {
		t.Fatal("unknown operation inherited readiness")
	}

	// Promoted by current evidence; its historical evidence class does not change with that.
	readiness, ok := ReadinessOf("structure.operation_type.list")
	if !ok || readiness != ReadinessCurrentVerified {
		t.Fatalf("structure.operation_type.list readiness = %q, %v; want %s, true", readiness, ok, ReadinessCurrentVerified)
	}
	evidenceClass, ok := Odoo19EvidenceClassOf("structure.operation_type.list")
	if !ok || evidenceClass != Odoo19EvidenceNewerUnverified {
		t.Fatalf("structure.operation_type.list evidence class = %q, %v; want %s, true", evidenceClass, ok, Odoo19EvidenceNewerUnverified)
	}
}

func TestNewerOperationsRemainUnverified(t *testing.T) {
	records, err := currentConformanceRecords()
	if err != nil {
		t.Fatal(err)
	}
	current := make(map[string]struct{}, len(records))
	for _, record := range records {
		current[record.OperationID] = struct{}{}
	}
	for _, operationID := range Odoo19NewerUnverifiedOperations {
		readiness, ok := ReadinessOf(operationID)
		want := ReadinessUnverified
		if _, promoted := current[operationID]; promoted {
			want = ReadinessCurrentVerified
		}
		if !ok || readiness != want {
			t.Fatalf("ReadinessOf(%q) = %q, %v; want %s, true", operationID, readiness, ok, want)
		}
	}
}

// TestReleaseReadinessPassesOnceEveryCuratedOperationHasEvidence: the four Gate 2 retail reads that
// kept the gate red since c18a5d95 now carry current, oracle-agreed evidence, so no curated operation
// is UNVERIFIED. The gate stays fail-closed: removing one of their records turns it red again
// (TestReleaseReadinessFailsClosedWithoutARecord).
func TestReleaseReadinessPassesOnceEveryCuratedOperationHasEvidence(t *testing.T) {
	if err := ValidateReleaseReadiness(); err != nil {
		t.Fatalf("ValidateReleaseReadiness() = %v; want nil", err)
	}
	if readiness, ok := ReadinessOf("admin.module.install"); !ok || readiness != ReadinessCurrentVerified {
		t.Fatalf("admin.module.install readiness = %q, %v; want CURRENT_VERIFIED, true", readiness, ok)
	}
	if Odoo19CatalogProvenance.CatalogOperations != 105 || Odoo19CatalogProvenance.HistoricalLiveEvidence != 55 || Odoo19CatalogProvenance.NewerUnverified != 50 {
		t.Fatalf("historical provenance changed: %+v", Odoo19CatalogProvenance)
	}
}

func TestPromotedReadOperationsAreCurrent(t *testing.T) {
	promotedReadIDs := []string{
		"catalog.product_variant.describe",
		"catalog.product_variant.list",
		"crm.lead.describe",
		"crm.lead.list",
		"people.department.describe",
		"people.department.list",
		"procurement.order.describe",
		"procurement.rfq.list",
		"sales.order.describe",
		"sales.quotation.list",
		"schedule.event.describe",
		"schedule.event.list",
		"structure.company.list",
		"structure.location.list",
		"structure.module.list",
		"structure.warehouse.list",
		"work.milestone.describe",
		"work.milestone.list",
		"work.project.describe",
		"work.project.list",
		"work.task.describe",
		"work.task.list",
		"structure.website.list",
		"website.site.describe",
		"website.page.list",
		"website.menu.list",
		"inventory.location.list",
		"workforce.attendance.list",
		"workforce.calendar.list",
		"finance.invoice.line.describe",
		"finance.invoice.line.list",
		"finance.payment.describe",
		"finance.payment.list",
		"finance.taxevidence.list",
		"procurement.order.line.describe",
		"sales.order.line.describe",
		"inventory.move.describe",
		"inventory.transfer.describe",
		"workforce.leave.list",
	}
	if len(promotedReadIDs) != 39 {
		t.Fatalf("expected 39 promoted read IDs, got %d", len(promotedReadIDs))
	}
	for _, id := range promotedReadIDs {
		readiness, ok := ReadinessOf(id)
		if !ok || readiness != ReadinessCurrentVerified {
			t.Errorf("ReadinessOf(%q) = %q, %v; want CURRENT_VERIFIED, true", id, readiness, ok)
		}
	}

}

func TestConformanceManifestRejectsInvalidOrUnsafeRecords(t *testing.T) {
	validRecord := ConformanceRecord{
		OperationID:     "people.employee.list",
		ProviderVersion: "19.0-20260914",
		ImageDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Modules:         []string{"base", "hr"},
		Status:          string(ReadinessCurrentVerified),
		EvidenceRef:     "evidence/2026-09-14/people.employee.list.json",
		VerifiedAt:      "2026-09-14T08:30:00Z",
	}
	manifest := func(records ...ConformanceRecord) []byte {
		encoded, err := json.Marshal(conformanceManifest{SchemaVersion: conformanceManifestSchema, Records: records})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}

	if records, err := parseConformanceManifest(manifest(validRecord)); err != nil || len(records) != 1 {
		t.Fatalf("valid current record refused: records=%v err=%v", records, err)
	}

	unknown := validRecord
	unknown.OperationID = "not.a.catalog.operation"
	missingID := validRecord
	missingID.OperationID = ""
	missingVersion := validRecord
	missingVersion.ProviderVersion = ""
	version18 := validRecord
	version18.ProviderVersion = "18.0-20260914"
	badDigest := validRecord
	badDigest.ImageDigest = "odoo:19"
	badStatus := validRecord
	badStatus.Status = string(ReadinessHistoricalOnly)
	badTime := validRecord
	badTime.VerifiedAt = "yesterday"
	credentialURL := validRecord
	credentialURL.EvidenceRef = "https://operator:secret@example.test/evidence.json"
	traversalRef := validRecord
	traversalRef.EvidenceRef = "../evidence.json"
	absoluteRef := validRecord
	absoluteRef.EvidenceRef = "/tmp/evidence.json"
	emptyModules := validRecord
	emptyModules.Modules = []string{}
	duplicateModules := validRecord
	duplicateModules.Modules = []string{"base", "base"}
	whitespaceModule := validRecord
	whitespaceModule.Modules = []string{" base"}

	for name, document := range map[string][]byte{
		"unknown id":              manifest(unknown),
		"missing id":              manifest(missingID),
		"duplicate id":            manifest(validRecord, validRecord),
		"missing version":         manifest(missingVersion),
		"non-19 version":          manifest(version18),
		"invalid digest":          manifest(badDigest),
		"empty modules":           manifest(emptyModules),
		"duplicate module":        manifest(duplicateModules),
		"whitespace module":       manifest(whitespaceModule),
		"historical status":       manifest(badStatus),
		"invalid time":            manifest(badTime),
		"credential URL":          manifest(credentialURL),
		"traversal evidence ref":  manifest(traversalRef),
		"absolute evidence ref":   manifest(absoluteRef),
		"raw response keyword":    []byte(`{"schemaVersion":"odoo19.conformance/v1","records":[],"rawResponse":{"password":"secret"}}`),
		"token keyword":           []byte(`{"schemaVersion":"odoo19.conformance/v1","records":[],"token":"secret"}`),
		"cookie keyword":          []byte(`{"schemaVersion":"odoo19.conformance/v1","records":[],"cookie":"session"}`),
		"response body keyword":   []byte(`{"schemaVersion":"odoo19.conformance/v1","records":[],"response_body":"data"}`),
		"unknown field in root":   []byte(`{"schemaVersion":"odoo19.conformance/v1","records":[],"extraField":true}`),
		"unknown field in record": []byte(`{"schemaVersion":"odoo19.conformance/v1","records":[{"operationId":"people.employee.list","providerVersion":"19.0","imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","modules":["base"],"status":"CURRENT_VERIFIED","evidenceRef":"ref.json","verifiedAt":"2026-09-14T08:30:00Z","extraField":true}]}`),
		"multiple JSON documents": []byte(`{"schemaVersion":"odoo19.conformance/v1","records":[]}{"schemaVersion":"odoo19.conformance/v1","records":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConformanceManifest(document); err == nil {
				t.Fatalf("unsafe/invalid manifest accepted: %s", document)
			}
		})
	}
}

func TestReleaseReadinessFailsClosedWithoutARecord(t *testing.T) {
	records, err := currentConformanceRecords()
	if err != nil {
		t.Fatal(err)
	}
	kept := records[:0]
	for _, record := range records {
		if record.OperationID != "structure.operation_type.list" {
			kept = append(kept, record)
		}
	}
	manifestOnce.Do(func() {})
	saved := manifestRecords
	manifestRecords = kept
	defer func() { manifestRecords = saved }()

	err = ValidateReleaseReadiness()
	if !errors.Is(err, ErrCatalogNotReleaseReady) || !strings.Contains(err.Error(), "structure.operation_type.list") {
		t.Fatalf("ValidateReleaseReadiness() without the record = %v; want a refusal naming structure.operation_type.list", err)
	}
}
