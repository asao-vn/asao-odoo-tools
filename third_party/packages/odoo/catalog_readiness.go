package odoo

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// OperationReadiness states whether an operation has current evidence, only historical evidence,
// or no accepted evidence. It is intentionally separate from catalog research confidence.
type OperationReadiness string

const (
	ReadinessCurrentVerified OperationReadiness = "CURRENT_VERIFIED"
	ReadinessHistoricalOnly  OperationReadiness = "HISTORICAL_ONLY"
	ReadinessUnverified      OperationReadiness = "UNVERIFIED"
)

// ConformanceRecord is one current, independently checked provider result. The embedded manifest
// contains metadata and hashes only; provider responses and credential material are forbidden.
type ConformanceRecord struct {
	OperationID     string   `json:"operationId"`
	ProviderVersion string   `json:"providerVersion"`
	ImageDigest     string   `json:"imageDigest"`
	Modules         []string `json:"modules"`
	Status          string   `json:"status"`
	EvidenceRef     string   `json:"evidenceRef"`
	VerifiedAt      string   `json:"verifiedAt"`
	// DefinitionHash is OperationDefinitionHash of the operation as it was verified. When set, the
	// record counts only while the operation still has that definition: a regenerated surface
	// operation whose model, fields, method or wizard changed loses its evidence until it is
	// verified again. Records without it (the curated catalog's) are bound by review instead.
	DefinitionHash string `json:"definitionHash,omitempty"`
}

// OperationDefinitionHash fingerprints what an operation asks of the provider: its model, strategy,
// read projection, writable fields and their typing, relations, method and wizard. Labels, risk
// and capability are not part of it, because evidence does not depend on them.
func OperationDefinitionHash(op Odoo19Operation) string {
	definition := map[string]interface{}{
		"model": op.ProviderModel, "strategy": op.Strategy, "readFields": op.ReadFields,
		"optionalReadFields": op.OptionalReadFields, "providerDomain": op.ProviderDomain, "defaultSort": op.DefaultSort,
		"writeMode": op.WriteMode, "allowedFields": op.AllowedFields, "forcedValues": op.ForcedValues,
		"fieldAliases": op.FieldAliasesJSON, "relations": op.Relations, "inputTypes": op.InputTypes, "inputEnums": op.InputEnums,
		"method": op.Method, "methodArgs": op.MethodArgs, "wizardModel": op.WizardModel, "wizardMethod": op.WizardMethod,
		"wizardLinkField": op.WizardLinkField, "wizardLinkMulti": op.WizardLinkMulti, "wizardValues": op.WizardValues,
		"wizardActiveContext": op.WizardActiveContext, "wizardAllowedFields": op.WizardAllowedFields,
		"wizardContext": op.WizardContext, "wizardContextTarget": op.WizardContextTarget,
		"requiredModules": op.RequiredModules,
	}
	// Added after evidence was first bound: present only when set, so the definitions recorded
	// before it keep their fingerprint.
	if op.WizardOpenerMethod != "" {
		definition["wizardOpenerMethod"] = op.WizardOpenerMethod
		definition["wizardOpenerActs"] = op.WizardOpenerActs
		if len(op.WizardAlsoAnswers) > 0 {
			definition["wizardAlsoAnswers"] = op.WizardAlsoAnswers
		}
	}
	if len(op.InputPrefixes) > 0 {
		definition["inputPrefixes"] = op.InputPrefixes
	}
	if len(op.RequiredOnCreate) > 0 {
		definition["requiredOnCreate"] = op.RequiredOnCreate
	}
	if op.ReadNewRecord {
		definition["readNewRecord"] = true
	}
	if op.WizardStandalone {
		definition["wizardStandalone"] = true
	}
	if len(op.MethodKwargs) > 0 || len(op.MethodInputs) > 0 {
		definition["methodKwargs"] = op.MethodKwargs
		definition["methodInputs"] = op.MethodInputs
		definition["methodRequiredInputs"] = op.MethodRequiredInputs
	}
	if len(op.WriteContext) > 0 {
		definition["writeContext"] = op.WriteContext
	}
	if len(op.LineInputs) > 0 {
		definition["lineInputs"] = op.LineInputs
	}
	if op.ReportName != "" {
		definition["reportName"] = op.ReportName
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		panic("odoo: operation definition is not encodable: " + err.Error())
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// recordStillApplies reports whether a record's evidence still covers the operation's definition.
func recordStillApplies(record ConformanceRecord) bool {
	if record.DefinitionHash == "" {
		return true
	}
	op, ok := lookupOperation(record.OperationID, true)
	return ok && OperationDefinitionHash(op) == record.DefinitionHash
}

// CatalogReadinessReport is derived from the canonical catalog, the accepted historical split and
// the current conformance records. CatalogOperations is never copied into the manifest.
type CatalogReadinessReport struct {
	CatalogOperations int                 `json:"catalogOperations"`
	CurrentVerified   int                 `json:"currentVerified"`
	HistoricalOnly    int                 `json:"historicalOnly"`
	Unverified        int                 `json:"unverified"`
	Records           []ConformanceRecord `json:"records"`
}

const conformanceManifestSchema = "odoo19.conformance/v1"

type conformanceManifest struct {
	SchemaVersion string              `json:"schemaVersion"`
	Records       []ConformanceRecord `json:"records"`
}

//go:embed odoo19_conformance_manifest.json
var embeddedConformanceManifest []byte

var (
	manifestOnce    sync.Once
	manifestRecords []ConformanceRecord
	manifestErr     error
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	credentialURL   = regexp.MustCompile(`(?i)://[^/@[:space:]]+:[^/@[:space:]]+@`)
	// manifestOperationID is a record's operationId, which must name a catalog operation and so can
	// carry no material: finance.payment_token.create is an operation, not a token.
	manifestOperationID = regexp.MustCompile(`"operationId"\s*:\s*"[a-z0-9_.]+"`)
)

// ErrCatalogNotReleaseReady identifies a release gate blocked by UNVERIFIED operations.
var ErrCatalogNotReleaseReady = errors.New("odoo catalog is not release ready")

func currentConformanceRecords() ([]ConformanceRecord, error) {
	records, err := sharedConformanceRecords()
	return cloneConformanceRecords(records), err
}

// sharedConformanceRecords is the parsed manifest itself, for read-only use inside this package.
// ReadinessOf runs once per operation every time a CLI command tree is built (about 1,350 calls);
// cloning every record on each of those calls made building the tree quadratic.
func sharedConformanceRecords() ([]ConformanceRecord, error) {
	manifestOnce.Do(func() {
		manifestRecords, manifestErr = parseConformanceManifest(embeddedConformanceManifest)
	})
	return manifestRecords, manifestErr
}

// odoo20_conformance_manifest.json is the accepted evidence for the candidate 20.0 series, kept
// apart from the 19.0 manifest because evidence never crosses series. It stays empty until a
// conformance run against a 20.0 provider is accepted.
//
//go:embed odoo20_conformance_manifest.json
var embeddedOdoo20ConformanceManifest []byte

const odoo20ConformanceManifestSchema = "odoo20.conformance/v1"

var (
	odoo20ManifestOnce    sync.Once
	odoo20ManifestRecords []ConformanceRecord
	odoo20ManifestErr     error
)

// seriesConformanceRecords returns the accepted records of one series; ok is false for a series
// with no manifest.
func seriesConformanceRecords(series string) (records []ConformanceRecord, ok bool, err error) {
	switch series {
	case Odoo19ProviderSeries:
		records, err = currentConformanceRecords()
		return records, true, err
	case Odoo20ProviderSeries:
		odoo20ManifestOnce.Do(func() {
			odoo20ManifestRecords, odoo20ManifestErr = parseSeriesConformanceManifest(embeddedOdoo20ConformanceManifest, odoo20ConformanceManifestSchema, Odoo20ProviderSeries)
		})
		return cloneConformanceRecords(odoo20ManifestRecords), true, odoo20ManifestErr
	}
	return nil, false, nil
}

func parseConformanceManifest(document []byte) ([]ConformanceRecord, error) {
	return parseSeriesConformanceManifest(document, conformanceManifestSchema, Odoo19ProviderSeries)
}

func parseSeriesConformanceManifest(document []byte, schema, series string) ([]ConformanceRecord, error) {
	lower := strings.ToLower(manifestOperationID.ReplaceAllString(string(document), `"operationId":""`))
	for _, forbidden := range []string{"password", "token", "cookie", "rawresponse", "raw_response", "responsebody", "response_body"} {
		if strings.Contains(lower, forbidden) {
			return nil, fmt.Errorf("odoo conformance manifest contains forbidden material %s", forbidden)
		}
	}
	if credentialURL.Match(document) {
		return nil, errors.New("odoo conformance manifest contains URL credential material")
	}

	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var manifest conformanceManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode odoo conformance manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("odoo conformance manifest must contain exactly one JSON document")
	}
	if manifest.SchemaVersion != schema {
		return nil, fmt.Errorf("odoo conformance manifest schema %q is not %q", manifest.SchemaVersion, schema)
	}
	if manifest.Records == nil {
		return nil, errors.New("odoo conformance manifest records must be an array")
	}

	seen := make(map[string]struct{}, len(manifest.Records))
	for index, record := range manifest.Records {
		if strings.TrimSpace(record.OperationID) == "" {
			return nil, fmt.Errorf("odoo conformance record %d has no catalog operation id", index)
		}
		if _, ok := lookupOperation(record.OperationID, true); !ok {
			return nil, fmt.Errorf("odoo conformance record %d names unknown catalog operation %q", index, record.OperationID)
		}
		if _, duplicate := seen[record.OperationID]; duplicate {
			return nil, fmt.Errorf("odoo conformance manifest duplicates operation %q", record.OperationID)
		}
		seen[record.OperationID] = struct{}{}
		if err := validateSeriesConformanceRecord(record, series); err != nil {
			return nil, err
		}
	}
	return cloneConformanceRecords(manifest.Records), nil
}

// ValidateConformanceRecord checks that a single conformance record satisfies all
// canonical catalog and evidence rules: known catalog operation, valid Odoo 19 version,
// valid image digest, non-empty and non-duplicate modules, status CURRENT_VERIFIED,
// safe relative evidence reference, and RFC 3339 verifiedAt timestamp.
func ValidateConformanceRecord(record ConformanceRecord) error {
	return validateSeriesConformanceRecord(record, Odoo19ProviderSeries)
}

// validateSeriesConformanceRecord applies the same rules for the series a manifest belongs to: the
// record's provider version must be a build of exactly that series.
func validateSeriesConformanceRecord(record ConformanceRecord, series string) error {
	if strings.TrimSpace(record.OperationID) == "" {
		return errors.New("odoo conformance record has no catalog operation id")
	}
	if _, ok := lookupOperation(record.OperationID, true); !ok {
		return fmt.Errorf("odoo conformance record names unknown catalog operation %q", record.OperationID)
	}
	if (ProviderVersionInfo{ServerVersion: record.ProviderVersion}).Series() != series {
		return fmt.Errorf("odoo conformance record %q has invalid provider version", record.OperationID)
	}
	if !digestPattern.MatchString(record.ImageDigest) {
		return fmt.Errorf("odoo conformance record %q has invalid image digest", record.OperationID)
	}
	if len(record.Modules) == 0 {
		return fmt.Errorf("odoo conformance record %q has no module metadata", record.OperationID)
	}
	modules := map[string]struct{}{}
	for _, module := range record.Modules {
		if module != strings.TrimSpace(module) || module == "" {
			return fmt.Errorf("odoo conformance record %q has an invalid module name", record.OperationID)
		}
		if _, duplicate := modules[module]; duplicate {
			return fmt.Errorf("odoo conformance record %q duplicates module %q", record.OperationID, module)
		}
		modules[module] = struct{}{}
	}
	if record.Status != string(ReadinessCurrentVerified) {
		return fmt.Errorf("odoo conformance record %q has non-current status %q", record.OperationID, record.Status)
	}
	if record.EvidenceRef == "" || strings.Contains(record.EvidenceRef, "://") || strings.HasPrefix(record.EvidenceRef, "/") || strings.Contains(record.EvidenceRef, "..") {
		return fmt.Errorf("odoo conformance record %q has unsafe evidence reference", record.OperationID)
	}
	if _, err := time.Parse(time.RFC3339, record.VerifiedAt); err != nil {
		return fmt.Errorf("odoo conformance record %q has invalid verification time", record.OperationID)
	}
	return nil
}

// NewCandidateConformanceRecord creates and validates a candidate ConformanceRecord.
// Status is always set to CURRENT_VERIFIED. Modules are deduplicated and sorted.
func NewCandidateConformanceRecord(operationID, providerVersion, imageDigest string, modules []string, evidenceRef, verifiedAt string) (ConformanceRecord, error) {
	cleanModules := canonicalStringSet(modules)
	rec := ConformanceRecord{
		OperationID:     operationID,
		ProviderVersion: providerVersion,
		ImageDigest:     imageDigest,
		Modules:         cleanModules,
		Status:          string(ReadinessCurrentVerified),
		EvidenceRef:     evidenceRef,
		VerifiedAt:      verifiedAt,
	}
	if err := ValidateConformanceRecord(rec); err != nil {
		return ConformanceRecord{}, err
	}
	return rec, nil
}

// ReadinessFor reports an operation's readiness on one provider series. The 19.0 baseline reports
// ReadinessOf. Any other series is CURRENT_VERIFIED only for an operation its own manifest covers
// and UNVERIFIED otherwise: evidence gathered on one series is never inherited by another, and a
// dialect is not evidence. ok is false for an unknown operation or a series with no manifest.
func ReadinessFor(series, operationID string) (OperationReadiness, bool) {
	if series == Odoo19ProviderSeries {
		return ReadinessOf(operationID)
	}
	if _, known := lookupOperation(operationID, true); !known {
		return "", false
	}
	records, hasManifest, err := seriesConformanceRecords(series)
	if err != nil {
		panic(err)
	}
	if !hasManifest {
		return "", false
	}
	for _, record := range records {
		if record.OperationID == operationID && recordStillApplies(record) {
			return ReadinessCurrentVerified, true
		}
	}
	return ReadinessUnverified, true
}

// ReadinessOf reports the current release readiness for a catalog operation. Unknown ids return
// ok=false rather than inheriting a default evidence state.
func ReadinessOf(operationID string) (OperationReadiness, bool) {
	if _, known := lookupOperation(operationID, true); !known {
		return "", false
	}
	records, err := sharedConformanceRecords()
	if err != nil {
		panic(err)
	}
	for _, record := range records {
		if record.OperationID == operationID && recordStillApplies(record) {
			return ReadinessCurrentVerified, true
		}
	}
	evidence, _ := Odoo19EvidenceClassOf(operationID)
	if evidence == Odoo19EvidenceHistoricalLive {
		return ReadinessHistoricalOnly, true
	}
	return ReadinessUnverified, true
}

// CurrentConformanceReport derives every count from the canonical catalog and current records.
func CurrentConformanceReport() CatalogReadinessReport {
	records, err := currentConformanceRecords()
	if err != nil {
		panic(err)
	}
	report := CatalogReadinessReport{CatalogOperations: len(Catalog()), Records: records}
	for _, operation := range Catalog() {
		readiness, _ := ReadinessOf(operation.SemanticID)
		switch readiness {
		case ReadinessCurrentVerified:
			report.CurrentVerified++
		case ReadinessHistoricalOnly:
			report.HistoricalOnly++
		case ReadinessUnverified:
			report.Unverified++
		}
	}
	return report
}

// ValidateReleaseReadiness fails closed while any operation has no accepted current or historical
// evidence. Historical evidence remains labelled HISTORICAL_ONLY and is never promoted by this gate.
func ValidateReleaseReadiness() error {
	if _, err := currentConformanceRecords(); err != nil {
		return err
	}
	unverified := make([]string, 0)
	for _, operation := range Catalog() {
		readiness, _ := ReadinessOf(operation.SemanticID)
		if readiness == ReadinessUnverified {
			unverified = append(unverified, operation.SemanticID)
		}
	}
	if len(unverified) == 0 {
		return nil
	}
	sort.Strings(unverified)
	label := "operation"
	if len(unverified) != 1 {
		label = "operations"
	}
	return fmt.Errorf("%w: %d UNVERIFIED %s: %s", ErrCatalogNotReleaseReady, len(unverified), label, strings.Join(unverified, ", "))
}

func cloneConformanceRecords(records []ConformanceRecord) []ConformanceRecord {
	if records == nil {
		return nil
	}
	cloned := make([]ConformanceRecord, len(records))
	copy(cloned, records)
	for index := range cloned {
		cloned[index].Modules = append([]string(nil), records[index].Modules...)
	}
	return cloned
}
