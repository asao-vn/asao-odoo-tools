// Package standalone_test drives the REAL `aerp` command tree — standalone.Root(), the same tree
// cmd/aerp/main.go mounts.
//
// Every test here must run on a machine with no Odoo, no ASAO platform and no configuration: that is
// the whole claim of "standalone", so nothing in this file may depend on a live dependency. The
// tests that need a profile create one under t.TempDir() and point AERP_PROFILE_FILE at it.
package standalone_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

// AcceptedCatalogSize is the ONE place the accepted operation count is written down in this package.
// Every other assertion compares the two surfaces to each other and to direct, so this constant
// is the only thing that has to change if the catalog legitimately grows — and a change to it is a
// deliberate, reviewable act rather than a test quietly following a drifting implementation.
// The current catalog is 105 operations with 77 current records and 28 historical-only operations;
// none is UNVERIFIED since the 2026-10-10 oracle-backed read run promoted the four Gate 2 reads.
// The provenance lineage is 55 + 50.
const AcceptedCatalogSize = 105

// AcceptedSurfaceSize is the same deliberate act for the generated surface catalog
// (packages/odoo/odoo19_surface_catalog.gen.go): regenerating it changes this number on purpose,
// and every surface operation is UNVERIFIED until a conformance record names it. 614 -> 599 on
// 2026-10-10: the dialogs business methods answer with (packages/odoo/provider_dialog.go) added 9
// operations that answer them and dropped 3 buttons that only open a form or a serial dialog; 21
// buttons the inventory had read from embedded sub-views (methods of other models) are gone. 599 ->
// 634: the standard operations the curated catalog lacked for models it owns (CURATED_GAPS: company,
// journals, taxes, payment terms, calendars, leaves, ...), found by setting up ASAO's own company.
// 634 -> 644: the website operations its landing page needed (website.site.update, website.page.*,
// website.menu.*, and website.layout.*, held to five layout views, seven since the header's search
// and sign-in). 644 -> 648: website.media.*, the public files a page loads, held to /media/.
// 648 -> 654: guarded deletes for the records a business spec removes (absent: true): CRM stages
// and lost reasons, task stages, job positions, website menus and media. 654 -> 656: the Settings
// page (structure.settings.describe/update), held to its reviewed switches and choices. 656 -> 716:
// manual journal entries made with their lines, inventory counts, and notes, activities and files on
// the records a business talks about. 716 -> 723: printed reports (<entity>.print), rendered through a
// web session as the web client's Print menu does; 723 -> 727: the deployment system parameters
// (report.url, which a containerised Odoo needs for printed reports to keep their header and style).
// 727 -> 734: the business buttons of list views: work order start/pause/finish, unreserve a
// transfer, and reorder rules' Order Once, Automate and clearing a quantity set by hand.
// 734 -> 1050: the 19 parked Community apps (fleet, maintenance, recruitment, surveys, repairs,
// events, restaurant, purchase agreements, batches, loyalty, expiry, delivery, dropship,
// subcontracting, landed costs, margins) from a fresh lab19 inventory.
// 1050 -> 1034: sixteen navigation buttons left out (they open another list or a URL and change
// nothing).
const AcceptedSurfaceSize = 1034

// acceptedUnverifiedOperations are the curated operations allowed to be UNVERIFIED. It is empty: the
// four Gate 2 retail reads that were (c18a5d95) now carry oracle-agreed records, and the curated
// release gate passes. A new unverified curated operation fails
// TestReadinessIsPublishedAndFilterable until it is added here on purpose.
var acceptedUnverifiedOperations = []string{}

// AcceptedSurfaceCurrent is how many surface operations carry a current record: 240 reads whose
// CLI answer agreed with an independent provider oracle, 182 creates/updates whose written values
// a postcondition oracle read back exactly (2026-10-10: an empty lab, Odoo's demo data, then the
// same with Vietnam's localization and SInvoice modules), and 46 actions and wizards that effected
// the same transition as an independent web-client oracle on twin records. 249 + 199 since the
// standard operations generated for curated-owned models (CURATED_GAPS) were verified. 291 + 219 + 70
// since 2026-10-11: files, journal entries, counts, Settings, system parameters, website media and
// layouts, pricelist items and payments, once the read oracle honored each operation's provider domain;
// and notes, activities, posting and resetting entries, applying and clearing counts. + 7: the prints,
// whose PDFs matched the browser's download page for page and word for word. + 6: the guarded deletes.
// Actions 70 -> 77: the list views' work order start/pause/finish, unreserve and reorder rules'
// replenish/automate/clear, on fresh twins.
// 2026-10-11, the parked apps: reads 291 -> 401, writes 219 -> 317, actions and wizards 77 -> 88,
// after the 51 operations their forms changed ran again.
// Then seeded models, updates on existing records and no overridden inputs: reads 419, writes 346.
// Then twelve actions of the parked apps on fixtures: actions and wizards 88 -> 100.
const AcceptedSurfaceCurrent = 419 + 346 + 100 + 7 + 6

func run(t *testing.T, args ...string) (int, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.Root(), args, clicore.Streams{Out: out, Diagnostics: diag})
	return code, out, diag
}

func decode(t *testing.T, out *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var envelope struct {
		APIVersion string                 `json:"apiVersion"`
		Kind       string                 `json:"kind"`
		Command    string                 `json:"command"`
		Status     string                 `json:"status"`
		Data       map[string]interface{} `json:"data"`
		Error      map[string]interface{} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if envelope.Error != nil {
		t.Fatalf("command failed: %v", envelope.Error)
	}
	return envelope.Data
}

func decodeError(t *testing.T, out *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var envelope struct {
		Error map[string]interface{} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if envelope.Error == nil {
		t.Fatalf("expected an error envelope, got: %s", out.String())
	}
	return envelope.Error
}

// writeProfiles points AERP_PROFILE_FILE at a fresh store for the duration of one test and returns
// its path. Nothing here writes credential material — that is what the credential ENV is for.
func writeProfiles(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(localprofile.FileVariable, path)
	return path
}

// ---------------------------------------------------------------------------
// PRODUCT IDENTITY
// ---------------------------------------------------------------------------

// TestVersionIdentifiesTheStandaloneProduct pins the one field an autonomous caller should branch on
// to tell the two binaries apart. A tool that cannot answer "am I the governed product?" mechanically
// is a tool an agent will eventually assume is governed.
func TestVersionIdentifiesTheStandaloneProduct(t *testing.T) {
	code, out, _ := run(t, "version", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := decode(t, out)
	if data["product"] != "STANDALONE_AERP" {
		t.Fatalf("product = %v, want STANDALONE_AERP", data["product"])
	}
	if data["program"] != standalone.Program {
		t.Fatalf("program = %v, want %s", data["program"], standalone.Program)
	}
	if data["governance"] != "NONE" {
		t.Fatalf("governance = %v, want NONE", data["governance"])
	}
}

// TestEnvelopeNeverClaimsToBeAsao is the program-name seam's regression: every document, error and
// remediation this binary emits must name THIS executable. A standalone tool that tells its caller
// to run `asao ...` is telling them to run a binary they may not have, in a product with different
// semantics.
func TestEnvelopeNeverClaimsToBeAsao(t *testing.T) {
	for _, args := range [][]string{
		{"version", "--output", "json"},
		{"catalog", "list", "--output", "json"},
		{"trust", "--output", "json"},
		{"commands", "list", "--output", "json"},
	} {
		_, out, _ := run(t, args...)
		var envelope struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(envelope.Command, standalone.Program+" ") {
			t.Fatalf("envelope command %q does not start with %q", envelope.Command, standalone.Program)
		}
	}
	// An unknown command's remediation is the case most likely to have been left hard-coded, because
	// it is the one nobody exercises by hand.
	_, out, _ := run(t, "nonsense", "--output", "json")
	failure := decodeError(t, out)
	if remediation, _ := failure["remediation"].(string); strings.Contains(remediation, "asao ") {
		t.Fatalf("remediation tells the caller to run asao: %q", remediation)
	}
}

// ---------------------------------------------------------------------------
// NO ASAO GOVERNANCE, PUBLISHED AS DATA
// ---------------------------------------------------------------------------

// TestNoLeafDeclaresACapability is the machine-readable form of
// ASAO_CAPABILITY_GRANT_PRESENT_IN_STANDALONE = NO. It walks the WHOLE discovery tree rather than a
// list of known commands, so a future command cannot acquire a capability declaration unnoticed.
func TestNoLeafDeclaresACapability(t *testing.T) {
	tree := discoveryTree(t)
	walk(tree, func(spec clicore.CommandSpec) {
		if spec.Group {
			return
		}
		if spec.RequiredCapability != "" {
			t.Fatalf("%s publishes requiredCapability %q; this product enforces none", spec.Command, spec.RequiredCapability)
		}
		if spec.CapabilityResolvedFrom != "" {
			t.Fatalf("%s publishes capabilityResolvedFrom %q; this product enforces none", spec.Command, spec.CapabilityResolvedFrom)
		}
		if spec.Authentication != string(clicore.AuthenticationNone) {
			t.Fatalf("%s publishes authentication %q, want NONE — there is no actor to authenticate", spec.Command, spec.Authentication)
		}
		if spec.ExecutionBoundary != clicore.ExecutionBoundaryLocalOperator {
			t.Fatalf("%s publishes executionBoundary %q, want LOCAL_OPERATOR", spec.Command, spec.ExecutionBoundary)
		}
		if spec.ScopeKind != "local-profile" {
			t.Fatalf("%s publishes scopeKind %q, want local-profile", spec.Command, spec.ScopeKind)
		}
		if spec.Destructive == nil {
			t.Fatalf("%s omits destructive; an agent must be able to read it rather than infer it", spec.Command)
		}
	})
}

// TestNoLeafClaimsToWriteCanonicalState guards a machine-readable claim that would be false here.
// EffectWriteCanonical means "writes ASAO canonical state", and this product has none: `aerp profile
// set` writes a local file, which is WRITE_LOCAL_CONFIG. A caller reasoning about blast radius from
// the discovery document must not be told a config edit touches the platform's system of record.
func TestNoLeafClaimsToWriteCanonicalState(t *testing.T) {
	tree := discoveryTree(t)
	walk(tree, func(spec clicore.CommandSpec) {
		if spec.Group {
			return
		}
		if spec.SideEffect == clicore.EffectWriteCanonical {
			t.Fatalf("%s declares sideEffect WRITE_CANONICAL; this product writes no ASAO canonical state", spec.Command)
		}
	})
}

// TestNoASAOOrganizationOrConnectionFlagExists proves the standalone command line cannot even
// EXPRESS an ASAO domain object. This is the flag-level counterpart of the import-boundary test: the
// binary cannot reach canonical state, and its surface does not ask for it either.
func TestNoASAOOrganizationOrConnectionFlagExists(t *testing.T) {
	forbidden := map[string]string{
		"organization": "an ASAO Organization is canonical state this product has none of",
		"connection":   "an ASAO ProviderConnection is canonical state this product has none of",
		"entity-kind":  "canonical business identity needs an ASAO BusinessEntityBinding",
		"entity-id":    "canonical business identity needs an ASAO BusinessEntityBinding",
		"actor":        "there is no ASAO actor in this product",
		"capability":   "there is no capability grant in this product",
	}
	tree := discoveryTree(t)
	walk(tree, func(spec clicore.CommandSpec) {
		for _, flag := range spec.Flags {
			if why, bad := forbidden[flag.Name]; bad {
				t.Fatalf("%s publishes --%s: %s", spec.Command, flag.Name, why)
			}
		}
	})
}

// TestTrustBoundaryIsPublishedAndNegative checks the disclosure states its ABSENCES positively, so a
// caller can assert on them. A document that only lists what a tool does provides no way to
// mechanically discover what it does not.
func TestTrustBoundaryIsPublishedAndNegative(t *testing.T) {
	code, out, _ := run(t, "trust", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := decode(t, out)
	absent, ok := data["absentByDesign"].(map[string]interface{})
	if !ok {
		t.Fatal("trust document publishes no absentByDesign block")
	}
	for _, key := range []string{
		"asaoAccountRequired", "asaoOrganizationRequired", "capabilityGrantEvaluated",
		"asaoAuthorityRuntimeUsed", "asaoCanonicalDatabaseUsed", "edgeRoutingAvailable",
		"managedOdooProvisioning",
	} {
		value, present := absent[key]
		if !present {
			t.Fatalf("trust document omits %s", key)
		}
		if value != false {
			t.Fatalf("trust document reports %s = %v, want false", key, value)
		}
	}
	notProvided, _ := data["doesNotProvide"].([]interface{})
	if len(notProvided) < 5 {
		t.Fatalf("doesNotProvide lists only %d items", len(notProvided))
	}
}

// ---------------------------------------------------------------------------
// ONE CATALOG, TWO SURFACES
// ---------------------------------------------------------------------------

// TestCatalogIsTheCanonicalCatalog compares the standalone surface's catalog to direct's own
// projection, operation by operation. Passing this while holding a private copy would require that
// copy to be byte-identical to the canonical one at every field — at which point the accompanying
// single-source check (scripts/checks/aerp-single-catalog.sh, which deletes an operation from the
// canonical source and rebuilds) is what catches it.
func TestCatalogIsTheCanonicalCatalog(t *testing.T) {
	code, out, _ := run(t, "catalog", "list", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := decode(t, out)
	items, _ := data["items"].([]interface{})
	canonical := direct.AllOperationIDs()

	if len(items) != len(canonical) {
		t.Fatalf("standalone catalog lists %d operations, direct defines %d", len(items), len(canonical))
	}
	if curated := len(direct.OperationIDs()); curated != AcceptedCatalogSize {
		t.Fatalf("curated catalog defines %d operations, accepted size is %d", curated, AcceptedCatalogSize)
	}
	if surface := len(direct.SurfaceOperations()); surface != AcceptedSurfaceSize {
		t.Fatalf("surface catalog defines %d operations, accepted size is %d", surface, AcceptedSurfaceSize)
	}
	seen := map[string]bool{}
	for _, raw := range items {
		item, _ := raw.(map[string]interface{})
		id, _ := item["id"].(string)
		seen[id] = true
	}
	for _, id := range canonical {
		if !seen[id] {
			t.Fatalf("canonical operation %q is missing from the standalone catalog", id)
		}
	}
}

// TestDescribeMatchesTheCanonicalContractForEveryOperation is the semantic-parity proof at contract
// level: for EVERY operation, what this surface publishes is byte-identical to what direct
// publishes — which is what the governed surface publishes too, since it calls the same function.
func TestDescribeMatchesTheCanonicalContractForEveryOperation(t *testing.T) {
	for _, id := range direct.OperationIDs() {
		code, out, _ := run(t, "catalog", "describe", "--operation", id, "--output", "json")
		if code != clicore.ExitOK {
			t.Fatalf("describe %s exited %d: %s", id, code, out)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		operation, err := direct.DescribeOperation(id)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := json.Marshal(operation)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonEqual(t, envelope.Data, expected) {
			t.Fatalf("describe %s does not match the canonical contract\n got: %s\nwant: %s", id, envelope.Data, expected)
		}
	}
}

// TestCatalogPublishesThatItEnforcesNoCapability guards the one field most likely to be misread.
// requiredCapability is published per operation because it is a property of the catalog; the same
// document must say, in machine-readable form, that nothing here evaluates it.
func TestCatalogPublishesThatItEnforcesNoCapability(t *testing.T) {
	_, out, _ := run(t, "catalog", "list", "--output", "json")
	data := decode(t, out)
	enforcement, ok := data["capabilityEnforcement"].(map[string]interface{})
	if !ok {
		t.Fatal("catalog list publishes no capabilityEnforcement block")
	}
	if enforcement["status"] != "NOT_ENFORCED_STANDALONE" {
		t.Fatalf("capabilityEnforcement.status = %v, want NOT_ENFORCED_STANDALONE", enforcement["status"])
	}
}

func TestReadinessIsPublishedAndFilterable(t *testing.T) {
	code, out, _ := run(t, "catalog", "list", "--readiness", "UNVERIFIED", "--origin", "CURATED", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := decode(t, out)
	items, _ := data["items"].([]interface{})
	listed := make([]string, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]interface{})
		id, _ := item["id"].(string)
		listed = append(listed, id)
	}
	sort.Strings(listed)
	if strings.Join(listed, ",") != strings.Join(acceptedUnverifiedOperations, ",") || data["count"] != float64(len(acceptedUnverifiedOperations)) {
		t.Fatalf("unverified list count=%v items=%v, want exactly %v", data["count"], listed, acceptedUnverifiedOperations)
	}

	// Every surface operation is UNVERIFIED and SURFACE_GENERATED, and none of them leaks into the
	// curated readiness report below.
	_, out, _ = run(t, "catalog", "list", "--origin", "SURFACE", "--output", "json")
	surface := decode(t, out)
	if surface["count"] != float64(AcceptedSurfaceSize) {
		t.Fatalf("surface list count=%v, want %d", surface["count"], AcceptedSurfaceSize)
	}
	current := 0
	for _, raw := range surface["items"].([]interface{}) {
		item, _ := raw.(map[string]interface{})
		if item["evidenceClass"] != "SURFACE_GENERATED" || item["surface"] != true {
			t.Fatalf("surface operation published as %v", item)
		}
		switch item["readiness"] {
		case "CURRENT_VERIFIED":
			current++
			// Reads, writes, actions and wizards, and prints each have an oracle-backed run; that an
			// action or a print is promoted only on its own run's evidence is pinned in packages/odoo.
			switch item["strategy"] {
			case "READ", "FIELD_WRITE", "METHOD_CALL", "WIZARD", "REPORT", "DESTRUCTIVE_GUARDED":
			default:
				t.Fatalf("surface operation %v is CURRENT_VERIFIED with strategy %v, which has no evidence run", item["id"], item["strategy"])
			}
		case "UNVERIFIED":
		default:
			t.Fatalf("surface operation published as %v", item)
		}
	}
	if current != AcceptedSurfaceCurrent {
		t.Fatalf("%d surface operations are CURRENT_VERIFIED, want %d", current, AcceptedSurfaceCurrent)
	}

	_, out, _ = run(t, "provenance", "--output", "json")
	readiness, _ := decode(t, out)["readiness"].(map[string]interface{})
	if readiness["catalogOperations"] != float64(AcceptedCatalogSize) || readiness["currentVerified"] != float64(103) || readiness["historicalOnly"] != float64(2) || readiness["unverified"] != float64(len(acceptedUnverifiedOperations)) {
		t.Fatalf("provenance readiness=%v", readiness)
	}

	_, out, _ = run(t, "doctor", "--output", "json")
	catalog, _ := decode(t, out)["catalog"].(map[string]interface{})
	if _, ok := catalog["readiness"].(map[string]interface{}); !ok {
		t.Fatalf("doctor catalog has no readiness report: %v", catalog)
	}
}

func TestDirectPlanAndExecuteRespectAdminPackBoundary(t *testing.T) {
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab"}}}`)
	t.Setenv("AERP_CREDENTIAL_LAB", "private-test-material")
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")

	code, out, _ := run(t, "plan", "--operation", "admin.module.install", "--provider-target-id", "1", "--output", "json")
	if code != clicore.ExitUsage {
		t.Fatalf("admin module plan exit=%d, want ExitUsage: %s", code, out)
	}
	failure := decodeError(t, out)
	if failure["code"] != "AERP_OPERATION_REFUSED" || failure["category"] != "USAGE" {
		t.Fatalf("admin module plan refusal=%v", failure)
	}
	if message, _ := failure["message"].(string); !strings.Contains(message, "capability pack") {
		t.Fatalf("admin module plan message=%q", message)
	}

	code, out, _ = run(t, "execute", "--operation", "admin.module.install", "--provider-target-id", "1", "--output", "json")
	if code != clicore.ExitUsage {
		t.Fatalf("admin module execute exit=%d, want ExitUsage: %s", code, out)
	}
	failure = decodeError(t, out)
	if failure["code"] != "AERP_OPERATION_REFUSED" || failure["category"] != "USAGE" {
		t.Fatalf("admin module execute refusal=%v", failure)
	}
	if message, _ := failure["message"].(string); !strings.Contains(message, "capability pack") {
		t.Fatalf("admin module execute message=%q", message)
	}
}

func TestDirectExecuteAdminModuleInstallCannotBypassPackBoundary(t *testing.T) {
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab"}}}`)
	t.Setenv("AERP_CREDENTIAL_LAB", "private-test-material")
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")

	code, out, _ := run(t, "execute", "--operation", "admin.module.install", "--provider-target-id", "1", "--allow-unverified", "--output", "json")
	if code != clicore.ExitUsage {
		t.Fatalf("admin module override exit=%d, want ExitUsage: %s", code, out)
	}
	failure := decodeError(t, out)
	if failure["code"] != "AERP_OPERATION_REFUSED" || failure["category"] != "USAGE" {
		t.Fatalf("admin module override refusal=%v", failure)
	}
	if message, _ := failure["message"].(string); !strings.Contains(message, "capability pack") {
		t.Fatalf("admin module override message=%q", message)
	}
}

func TestUnverifiedOverrideIsStandaloneCommandMetadata(t *testing.T) {
	tree := discoveryTree(t)
	want := map[string]bool{"aerp plan": false, "aerp execute": false}
	walk(tree, func(spec clicore.CommandSpec) {
		if _, ok := want[spec.Command]; !ok {
			return
		}
		for _, flag := range spec.Flags {
			if flag.Name == "allow-unverified" && flag.Kind == clicore.KindBool {
				want[spec.Command] = true
			}
		}
	})
	for command, found := range want {
		if !found {
			t.Fatalf("%s does not publish --allow-unverified", command)
		}
	}
}

// TestUnknownOperationNamesNeighboursNotAsao checks the shared error path renders under this
// product's own command names.
func TestUnknownOperationNamesNeighboursNotAsao(t *testing.T) {
	code, out, _ := run(t, "catalog", "describe", "--operation", "people.employee.nonsense", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("an unknown operation must not succeed")
	}
	failure := decodeError(t, out)
	if failure["code"] != "AERP_UNKNOWN_OPERATION" {
		t.Fatalf("code = %v, want AERP_UNKNOWN_OPERATION", failure["code"])
	}
	remediation, _ := failure["remediation"].(string)
	if !strings.Contains(remediation, "aerp catalog list") || strings.Contains(remediation, "asao ") {
		t.Fatalf("remediation names the wrong executable: %q", remediation)
	}
	context, _ := failure["context"].(map[string]interface{})
	if context["operationsInSameDomain"] == nil {
		t.Fatal("an unknown operation must name its domain's real operations")
	}
}

// ---------------------------------------------------------------------------
// RAW PROVIDER RPC IS ABSENT
// ---------------------------------------------------------------------------

// TestNoRawProviderRPCSurface is RAW_PROVIDER_RPC_PRIMARY_SURFACE = ABSENT, checked against the
// published tree rather than against memory. The product promise is the canonical catalog; a generic
// "call any model/method" command would quietly replace it as the thing agents actually use.
func TestNoRawProviderRPCSurface(t *testing.T) {
	tree := discoveryTree(t)
	walk(tree, func(spec clicore.CommandSpec) {
		name := spec.Path[len(spec.Path)-1]
		for _, forbidden := range []string{"rpc", "call", "raw", "exec-model", "xmlrpc", "jsonrpc"} {
			if name == forbidden {
				t.Fatalf("%s is a raw provider RPC surface; the canonical catalog is the product contract", spec.Command)
			}
		}
		for _, flag := range spec.Flags {
			switch flag.Name {
			case "model", "method", "odoo-model", "odoo-method":
				t.Fatalf("%s publishes --%s, which is raw provider RPC, not a canonical operation", spec.Command, flag.Name)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// PROFILES: PRECEDENCE, AMBIGUITY, ISOLATION
// ---------------------------------------------------------------------------

func TestProfileFileAbsentIsAConfigAnswerNotAFault(t *testing.T) {
	t.Setenv(localprofile.FileVariable, filepath.Join(t.TempDir(), "nothing.json"))
	code, out, _ := run(t, "profile", "list", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("listing profiles with no profile file must not succeed")
	}
	failure := decodeError(t, out)
	if failure["code"] != "PROFILE_FILE_NOT_FOUND" {
		t.Fatalf("code = %v, want PROFILE_FILE_NOT_FOUND", failure["code"])
	}
	if failure["category"] == "INTERNAL" {
		t.Fatal("an unconfigured machine is a configuration answer, never an internal fault")
	}
}

// TestSeveralProfilesWithNoSelectionIsRefusedNotGuessed is the §24 rule with teeth. Picking "the
// first" profile is the failure mode that eventually picks production.
func TestSeveralProfilesWithNoSelectionIsRefusedNotGuessed(t *testing.T) {
	writeProfiles(t, `{"version":"aerp.profiles/v1","profiles":{
	  "a":{"endpoint":"http://a.invalid","database":"a","login":"admin","credentialRef":"a"},
	  "b":{"endpoint":"http://b.invalid","database":"b","login":"admin","credentialRef":"b"}}}`)
	code, out, _ := run(t, "profile", "show", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("two profiles and no selection must be refused, not guessed")
	}
	failure := decodeError(t, out)
	if failure["code"] != "PROFILE_AMBIGUOUS" {
		t.Fatalf("code = %v, want PROFILE_AMBIGUOUS", failure["code"])
	}
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "a") || !strings.Contains(message, "b") {
		t.Fatalf("an ambiguity refusal must name every candidate: %q", message)
	}
}

// TestProfilePrecedenceOrder pins the documented order end to end, one step at a time.
func TestProfilePrecedenceOrder(t *testing.T) {
	document := `{"version":"aerp.profiles/v1","defaultProfile":"c","profiles":{
	  "a":{"endpoint":"http://a.invalid","database":"a","login":"admin","credentialRef":"a"},
	  "b":{"endpoint":"http://b.invalid","database":"b","login":"admin","credentialRef":"b"},
	  "c":{"endpoint":"http://c.invalid","database":"c","login":"admin","credentialRef":"c"}}}`

	t.Run("flag wins over environment and default", func(t *testing.T) {
		writeProfiles(t, document)
		t.Setenv(localprofile.SelectorVariable, "b")
		_, out, _ := run(t, "profile", "show", "--profile", "a", "--output", "json")
		data := decode(t, out)
		if data["name"] != "a" || data["selectedBy"] != localprofile.SourceFlag {
			t.Fatalf("selected %v by %v, want a by FLAG", data["name"], data["selectedBy"])
		}
	})
	t.Run("environment wins over the file default", func(t *testing.T) {
		writeProfiles(t, document)
		t.Setenv(localprofile.SelectorVariable, "b")
		_, out, _ := run(t, "profile", "show", "--output", "json")
		data := decode(t, out)
		if data["name"] != "b" || data["selectedBy"] != localprofile.SourceEnvironment {
			t.Fatalf("selected %v by %v, want b by ENVIRONMENT", data["name"], data["selectedBy"])
		}
	})
	t.Run("the file default is used when nothing else selects", func(t *testing.T) {
		writeProfiles(t, document)
		_, out, _ := run(t, "profile", "show", "--output", "json")
		data := decode(t, out)
		if data["name"] != "c" || data["selectedBy"] != localprofile.SourceFileDefault {
			t.Fatalf("selected %v by %v, want c by FILE_DEFAULT", data["name"], data["selectedBy"])
		}
	})
	t.Run("a sole profile is unambiguous", func(t *testing.T) {
		writeProfiles(t, `{"version":"aerp.profiles/v1","profiles":{
		  "only":{"endpoint":"http://only.invalid","database":"only","login":"admin","credentialRef":"only"}}}`)
		_, out, _ := run(t, "profile", "show", "--output", "json")
		data := decode(t, out)
		if data["name"] != "only" || data["selectedBy"] != localprofile.SourceSoleProfile {
			t.Fatalf("selected %v by %v, want only by SOLE_PROFILE", data["name"], data["selectedBy"])
		}
	})
}

// TestNoFallbackToProcessGlobalOdooVariables is the other half of §24: the legacy erp-adapter is
// configured by process-global ODOO_URL/ODOO_DB, and inheriting that here would let an unselected
// call reach an Odoo nobody named.
func TestNoFallbackToProcessGlobalOdooVariables(t *testing.T) {
	t.Setenv("ODOO_URL", "http://smuggled.invalid")
	t.Setenv("ODOO_DB", "smuggled")
	t.Setenv("ODOO_USER", "admin")
	t.Setenv("ODOO_PASSWORD", "smuggled-secret")
	t.Setenv(localprofile.FileVariable, filepath.Join(t.TempDir(), "nothing.json"))

	code, out, _ := run(t, "execute", "--operation", "people.employee.list", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("with no profile configured, an execution must refuse — not fall back to ODOO_* variables")
	}
	failure := decodeError(t, out)
	if failure["code"] != "PROFILE_FILE_NOT_FOUND" {
		t.Fatalf("code = %v, want PROFILE_FILE_NOT_FOUND", failure["code"])
	}
	if strings.Contains(out.String(), "smuggled") {
		t.Fatal("a process-global ODOO_* value reached the output")
	}
}

// ---------------------------------------------------------------------------
// CREDENTIALS NEVER APPEAR IN OUTPUT
// ---------------------------------------------------------------------------

// TestNoCommandEchoesCredentialMaterial runs every non-mutating command that touches a profile with a
// sentinel password in the environment and asserts the sentinel appears in NO stream. Checking both
// stdout and stderr matters: a diagnostic leak is still a leak, it is just one that JSON-parsing
// callers would not notice.
func TestNoCommandEchoesCredentialMaterial(t *testing.T) {
	const sentinel = "s3ntinel-password-do-not-log"
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab"}}}`)
	t.Setenv("AERP_CREDENTIAL_LAB", sentinel)

	for _, args := range [][]string{
		{"profile", "list", "--output", "json"},
		{"profile", "show", "--output", "json"},
		{"profile", "show", "--output", "text"},
		{"doctor", "--output", "json"},
		{"trust", "--output", "json"},
		{"commands", "list", "--output", "json"},
		{"plan", "--operation", "people.employee.list", "--output", "json"},
		{"execute", "--operation", "people.employee.list", "--output", "json"},
	} {
		_, out, diag := run(t, args...)
		if strings.Contains(out.String(), sentinel) {
			t.Fatalf("`aerp %s` echoed credential material on stdout", strings.Join(args, " "))
		}
		if strings.Contains(diag.String(), sentinel) {
			t.Fatalf("`aerp %s` echoed credential material on stderr", strings.Join(args, " "))
		}
	}
}

// TestProfileShowReportsCredentialAsABoolean checks the diagnostic answers "is it wired up" without
// answering "what is it".
func TestProfileShowReportsCredentialAsABoolean(t *testing.T) {
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab"}}}`)

	_, out, _ := run(t, "profile", "show", "--output", "json")
	data := decode(t, out)
	if data["credentialResolves"] != false {
		t.Fatalf("credentialResolves = %v with no material set, want false", data["credentialResolves"])
	}
	if data["credentialVariable"] != "AERP_CREDENTIAL_LAB" {
		t.Fatalf("credentialVariable = %v, want AERP_CREDENTIAL_LAB", data["credentialVariable"])
	}

	t.Setenv("AERP_CREDENTIAL_LAB", "anything")
	_, out, _ = run(t, "profile", "show", "--output", "json")
	if decode(t, out)["credentialResolves"] != true {
		t.Fatal("credentialResolves stayed false after material was supplied")
	}
}

// TestProfileProtocolIsRecordedAndShown: a profile speaks JSON-RPC unless it says json2, says which
// it speaks, and refuses any other protocol.
func TestProfileProtocolIsRecordedAndShown(t *testing.T) {
	path := writeProfiles(t, `{"version":"aerp.profiles/v1","profiles":{}}`)

	code, out, _ := run(t, "profile", "set", "--name", "lab", "--endpoint", "http://127.0.0.1:1", "--database", "lab",
		"--login", "admin", "--protocol", "json2", "--output", "json")
	if code != clicore.ExitOK || decode(t, out)["protocol"] != "json2" {
		t.Fatalf("profile set --protocol json2: exit=%d %s", code, out.String())
	}
	_, out, _ = run(t, "profile", "show", "--profile", "lab", "--output", "json")
	if decode(t, out)["protocol"] != "json2" {
		t.Fatalf("profile show = %s, want protocol json2", out.String())
	}

	if code, _, _ := run(t, "profile", "set", "--name", "lab", "--endpoint", "http://127.0.0.1:1", "--database", "lab",
		"--protocol", "xmlrpc", "--output", "json"); code == clicore.ExitOK {
		t.Fatal("an unknown protocol was accepted")
	}

	run(t, "profile", "set", "--name", "lab", "--endpoint", "http://127.0.0.1:1", "--database", "lab", "--output", "json")
	_, out, _ = run(t, "profile", "show", "--profile", "lab", "--output", "json")
	if decode(t, out)["protocol"] != "jsonrpc" {
		t.Fatalf("profile show = %s, want the default protocol jsonrpc", out.String())
	}
	if written, _ := os.ReadFile(path); strings.Contains(string(written), "protocol") {
		t.Fatalf("the default protocol was written, so older readers would see a field they do not know: %s", written)
	}

	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab","protocol":"soap"}}}`)
	if code, _, _ := run(t, "profile", "show", "--output", "json"); code == clicore.ExitOK {
		t.Fatal("a profile file naming an unknown protocol was used")
	}
}

// TestPermissiveCredentialFileIsRefusedNotRead: a credential file other local accounts can read is a
// posture the operator probably did not choose, and reading it anyway would silently accept it.
func TestPermissiveCredentialFileIsRefusedNotRead(t *testing.T) {
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab"}}}`)
	credentials := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credentials, []byte(`{"lab":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(localprofile.CredentialFileVariable, credentials)

	code, out, _ := run(t, "execute", "--operation", "people.employee.list", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("a world-readable credential file must be refused")
	}
	failure := decodeError(t, out)
	if failure["code"] != "PROVIDER_CREDENTIAL_FILE_PERMISSIVE" {
		t.Fatalf("code = %v, want PROVIDER_CREDENTIAL_FILE_PERMISSIVE", failure["code"])
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatal("the refused file's contents reached the output")
	}
}

// TestProfileSetRefusesToTakeAPassword: a secret on a command line ends up in shell history and in
// every other local user's view of the process table. There must be no flag that accepts one.
func TestProfileSetRefusesToTakeAPassword(t *testing.T) {
	tree := discoveryTree(t)
	walk(tree, func(spec clicore.CommandSpec) {
		for _, flag := range spec.Flags {
			switch flag.Name {
			case "password", "secret", "credential", "api-key", "token":
				t.Fatalf("%s publishes --%s, which would put a secret on the command line", spec.Command, flag.Name)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// EXECUTION SEMANTICS
// ---------------------------------------------------------------------------

// Canonical targeting is rejected by the direct command tree before profile resolution.
func TestEntityMediationIsRefusedNotDegraded(t *testing.T) {
	code, _, _ := run(t, "execute", "--operation", "people.employee.describe", "--entity-kind", "employee", "--entity-id", "e-1", "--purpose", "payroll", "--output", "json")
	if code != clicore.ExitUsage {
		t.Fatalf("canonical entity flags accepted: exit=%d", code)
	}
}

func TestStandaloneExecutorNeedsNoOrganization(t *testing.T) {
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")
	executor, err := direct.NewExecutor(context.Background(), direct.Target{
		ProfileName: "lab", Endpoint: "http://127.0.0.1:1", Database: "lab", Login: "admin", Password: "x",
	}, providerendpoint.CheckHost)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := executor.Plan(context.Background(), direct.Request{Operation: "people.employee.list", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProviderRoute.Resolution != direct.ResolutionLocalProfile || plan.ProviderRoute.ConnectionID != "lab" {
		t.Fatalf("route=%+v", plan.ProviderRoute)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "organizationId") {
		t.Fatal("direct plan contains ASAO identity")
	}
}

// TestPlanRefusesAnUnreachableProviderCleanly: the composition failure most operators will hit first
// must be a classified, actionable error rather than an internal fault.
func TestPlanRefusesAnUnreachableProviderCleanly(t *testing.T) {
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab"}}}`)
	t.Setenv("AERP_CREDENTIAL_LAB", "x")

	code, out, _ := run(t, "execute", "--operation", "people.employee.list", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("an execution against a closed port must not succeed")
	}
	failure := decodeError(t, out)
	if failure["category"] == "INTERNAL" {
		t.Fatalf("a closed provider port is not an internal fault: %v", failure)
	}
}

// TestTargetRulesComeFromTheCatalog checks the canonical pre-provider refusals are enforced here too
// — they are properties of the OPERATION, so they must not depend on which product is calling.
func TestTargetRulesComeFromTheCatalog(t *testing.T) {
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":"http://127.0.0.1:1","database":"lab","login":"admin","credentialRef":"lab"}}}`)
	t.Setenv("AERP_CREDENTIAL_LAB", "x")
	// The shared endpoint policy refuses loopback unless the operator opts in, and it is checked
	// during executor construction — before the catalog rules this test is about. Opting in here is
	// what every real local-Odoo user does, and it is what lets this test reach the rule it means to
	// exercise rather than stopping one layer earlier.
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")

	// Find, from the catalog itself, one operation that REQUIRES a target and run it without one.
	var required string
	for _, operation := range direct.Operations() {
		// admin.module.install is refused by aerpsurface before target rules (V2_12 capability packs); pick another target-requiring op
		if operation.ID == "admin.module.install" {
			continue
		}
		if operation.TargetRequirement == string(direct.TargetRequirementRequired) {
			required = operation.ID
			break
		}
	}
	if required == "" {
		t.Skip("the catalog defines no target-requiring operation")
	}
	code, out, _ := run(t, "plan", "--operation", required, "--output", "json")
	if code == clicore.ExitOK {
		t.Fatalf("%s requires a target and was accepted without one", required)
	}
	failure := decodeError(t, out)
	if failure["code"] != "AERP_TARGET_REQUIRED" {
		t.Fatalf("code = %v, want AERP_TARGET_REQUIRED", failure["code"])
	}
	remediation, _ := failure["remediation"].(string)
	if strings.Contains(remediation, "--entity-kind") {
		t.Fatalf("remediation offers canonical identity this product does not have: %q", remediation)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func discoveryTree(t *testing.T) clicore.CommandSpec {
	t.Helper()
	code, out, _ := run(t, "commands", "list", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("commands list exited %d: %s", code, out)
	}
	raw, err := json.Marshal(decode(t, out)["tree"])
	if err != nil {
		t.Fatal(err)
	}
	var tree clicore.CommandSpec
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("data.tree is not a CommandSpec: %v", err)
	}
	return tree
}

func walk(spec clicore.CommandSpec, visit func(clicore.CommandSpec)) {
	if len(spec.Path) > 0 {
		visit(spec)
	}
	for _, child := range spec.Children {
		walk(child, visit)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var left, right interface{}
	if err := json.Unmarshal(a, &left); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &right); err != nil {
		return false
	}
	leftEncoded, _ := json.Marshal(left)
	rightEncoded, _ := json.Marshal(right)
	return bytes.Equal(leftEncoded, rightEncoded)
}
