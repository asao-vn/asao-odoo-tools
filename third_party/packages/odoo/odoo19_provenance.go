package odoo

// Odoo19 catalog provenance — the evidence lineage of the reused semantic
// surface, stated as data so no consumer has to re-derive it (or, worse,
// invent it) from prose.
//
// SOURCE OF TRUTH, verbatim from Slice D's accepted implementation evidence
// (roadmap/frozen-architecture-implementation-gap-analysis/
// slice-d-aerp-odoo19-canonicalization/IMPLEMENTATION_EVIDENCE.md §
// "Historical evidence provenance"):
//
//	HISTORICAL_CONFORMANCE_RECORD_COUNT     = 55
//	NEWER_UNVERIFIED_OPERATIONS             = 3   (work.milestone.create,
//	                                               work.milestone.describe,
//	                                               work.milestone.list)
//
// P11 added three more operations to the catalog (structure.company.list,
// structure.warehouse.list, structure.location.list) and appended them to
// NEWER_UNVERIFIED_OPERATIONS below. V2_7 (DEC-2026-0096) added three workforce
// read operations (workforce.calendar.list, workforce.attendance.list,
// workforce.leave.list) as catalogued-not-live. V2_11 (DEC-2026-0097) added six website provider
// operations (structure.module.list, structure.website.list, website.site.describe, website.page.list,
// website.menu.list, admin.module.install) as catalogued-not-live. The historical record is unchanged
// and still covers 55. Historical additions moved the total from 58 through 71; the current
// converged catalog contains 105 operations and 50 newer-unverified operations (structure.operation_type.list,
// inventory.quant.list, inventory.move_line.list, catalog.uom.describe
// added as source-discovered). Nothing here
// upgrades any evidence.
//	HISTORICAL_PROVIDER_VERSION             = Odoo 19.0-20260803
//	HISTORICAL_LIVE_EVIDENCE                = FEATURE_LINEAGE_ONLY
//	CANONICAL_LIVE_PROVIDER_REVERIFICATION  = NOT_PERFORMED_IN_SLICE_D
//
// This file exists because the catalog's own Confidence field cannot carry
// that split: Confidence records how each operation was RESEARCHED
// (LIVE_WORKFLOW_VERIFIED / LIVE_READ_VERIFIED), which is a different claim
// from whether it appears in the 55-operation historical CONFORMANCE record.
// Publishing Confidence as if it were conformance provenance would silently
// claim 58/58 live-verified, which is false. Nothing here upgrades any
// evidence; it only makes the accepted split machine-readable.
//
// This is NOT a second operation catalog. It carries no operation semantics,
// no strategy, no fields — only the provenance labels attached to SemanticIDs
// that Catalog() already defines. TestProvenanceReferencesRealOperations
// pins that every ID named here exists in the catalog.

// Odoo19EvidenceClass is how a catalog operation stands relative to the
// accepted historical live-conformance record.
type Odoo19EvidenceClass string

const (
	// Odoo19EvidenceHistoricalLive: the operation is covered by the
	// 55-operation historical LAB conformance record described above. The
	// evidence is FEATURE_LINEAGE_ONLY and was NOT re-verified on canonical
	// lineage — it is prior art referenced, not re-gathered.
	Odoo19EvidenceHistoricalLive Odoo19EvidenceClass = "HISTORICAL_LIVE_EVIDENCE"
	// Odoo19EvidenceNewerUnverified: the operation was added after that
	// conformance record was produced and has no live evidence of any
	// lineage. Static catalog presence is not evidence of execution.
	Odoo19EvidenceNewerUnverified Odoo19EvidenceClass = "NEWER_UNVERIFIED"
	// Odoo19EvidenceOtherSeries labels an execution against a series other than 19.0. The 19.0
	// lineage above says nothing about it; that series' own readiness (ReadinessFor) does.
	Odoo19EvidenceOtherSeries Odoo19EvidenceClass = "OTHER_SERIES"
)

// Odoo19HistoricalProviderVersion is the provider build the historical
// conformance record was produced against.
const Odoo19HistoricalProviderVersion = "19.0-20260803"

// Odoo19NewerUnverifiedOperations are the operations added after the
// historical conformance record. Named individually, in the accepted
// document's own order, rather than derived by subtraction — subtraction
// would silently relabel operations if the catalog ever grows.
//
// The first three are Slice D's milestone operations. The last three are
// P11's provider structural-discovery reads, and they are listed here for
// exactly the reason this file exists: they were added long after the
// conformance sweep, so labelling them HISTORICAL_LIVE_EVIDENCE by omission
// would have manufactured evidence that does not exist. P11's live gate does
// run all three against a real Odoo 19 — but that is P11's own evidence, not
// the accepted historical conformance record this record describes, and the
// two must not be conflated.
var Odoo19NewerUnverifiedOperations = []string{
	"work.milestone.create",
	"work.milestone.describe",
	"work.milestone.list",
	"structure.company.list",
	"structure.warehouse.list",
	"structure.location.list",
	// finance.taxevidence.list was added by the VN compliance vertical. It IS live-read
	// verified against a real Odoo 19 (its catalog comment records the exact measured
	// values), but that is THIS milestone's own evidence, gathered 2026-08-25 — it is not
	// part of the 55-operation historical conformance record, and labelling it
	// HISTORICAL_LIVE_EVIDENCE by omitting it here would manufacture a lineage it does not
	// have. Same distinction P11's three structural reads are held to above.
	"finance.taxevidence.list",
	// workforce.* read operations added in V2_7 (DEC-2026-0096) — catalogued not live-verified.
	"workforce.calendar.list",
	"workforce.attendance.list",
	"workforce.leave.list",
	// Historical 71/16 milestone: +6 website provider ops (V2_11, DEC-2026-0097, catalogued not
	// live-verified). The current converged total and split are published above.
	"structure.module.list",
	"structure.website.list",
	"website.site.describe",
	"website.page.list",
	"website.menu.list",
	"admin.module.install",

	// The following were brought over from the Wave 2 Odoo CLI lineage when the semantic core
	// converged (V2.5 Gate #1). That lineage records its own 92/92 conformance run, but that
	// evidence was gathered THERE and was never re-gathered on canonical lineage. Labelling these
	// HISTORICAL_LIVE_EVIDENCE by omitting them would manufacture exactly the lineage this file
	// exists to refuse. They are NEWER_UNVERIFIED here until canonical evidence exists.
	"catalog.product_variant.describe",
	"catalog.product_variant.list",
	"crm.lead.update",
	"finance.invoice.cancel",
	"finance.invoice.draft",
	"finance.invoice.line.describe",
	"finance.invoice.line.list",
	"finance.payment.describe",
	"finance.payment.list",
	"identity.contact.update",
	"inventory.location.list",
	"inventory.move.describe",
	"inventory.transfer.cancel",
	"inventory.transfer.describe",
	"people.department.describe",
	"procurement.order.cancel",
	"procurement.order.describe",
	"procurement.order.draft",
	"procurement.order.line.describe",
	"procurement.order.line.update",
	"procurement.rfq.update",
	"sales.order.cancel",
	"sales.order.draft",
	"sales.order.line.describe",
	"sales.order.line.update",
	"sales.quotation.update",
	"schedule.event.describe",
	"schedule.event.update",
	"work.project.describe",
	"work.project.update",
	"structure.operation_type.list",
	"inventory.quant.list",
	"inventory.move_line.list",
	"catalog.uom.describe",
}

// Odoo19CatalogProvenanceRecord is the whole provenance claim in one value,
// for consumers (the canonical CLI included) that must publish it without
// paraphrasing.
type Odoo19CatalogProvenanceRecord struct {
	// CatalogOperations is the total number of operations the catalog defines.
	CatalogOperations int
	// HistoricalLiveEvidence is how many of them the historical conformance
	// record covers.
	HistoricalLiveEvidence int
	// NewerUnverified is how many have no live evidence at all.
	NewerUnverified int
	// NewerUnverifiedOperations names them.
	NewerUnverifiedOperations []string
	// HistoricalProviderVersion is the provider build behind the evidence.
	HistoricalProviderVersion string
	// HistoricalEvidenceLineage records that the evidence lives on the
	// feature lineage, not on canonical lineage.
	HistoricalEvidenceLineage string
	// CanonicalLiveReverification records that canonical lineage has not
	// re-run the conformance sweep.
	CanonicalLiveReverification string
}

// Odoo19CatalogProvenance is the single provenance value every consumer must
// publish. It is computed from the catalog's real length so the total can
// never drift away from the catalog it describes, while the historical split
// stays pinned to the accepted document.
var Odoo19CatalogProvenance = Odoo19CatalogProvenanceRecord{
	CatalogOperations:           len(Catalog()),
	HistoricalLiveEvidence:      len(Catalog()) - len(Odoo19NewerUnverifiedOperations),
	NewerUnverified:             len(Odoo19NewerUnverifiedOperations),
	NewerUnverifiedOperations:   Odoo19NewerUnverifiedOperations,
	HistoricalProviderVersion:   Odoo19HistoricalProviderVersion,
	HistoricalEvidenceLineage:   "FEATURE_LINEAGE_ONLY",
	CanonicalLiveReverification: "NOT_PERFORMED_IN_SLICE_D",
}

// Odoo19EvidenceClassOf reports how a semantic operation stands relative to
// the historical conformance record. Catalog readiness maps historical live evidence to
// HISTORICAL_ONLY and newer-unverified evidence to UNVERIFIED unless an explicit current
// conformance record exists; it never upgrades this historical label. An operation the catalog does not
// define returns ok=false rather than a default class — an unknown id must
// never silently inherit "historically verified".
func Odoo19EvidenceClassOf(semanticID string) (Odoo19EvidenceClass, bool) {
	if _, known := CatalogByID[semanticID]; !known {
		if IsSurfaceOperation(semanticID) {
			return Odoo19EvidenceSurfaceGenerated, true
		}
		return "", false
	}
	for _, id := range Odoo19NewerUnverifiedOperations {
		if id == semanticID {
			return Odoo19EvidenceNewerUnverified, true
		}
	}
	return Odoo19EvidenceHistoricalLive, true
}
