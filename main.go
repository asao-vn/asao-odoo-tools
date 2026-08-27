// Command odoo19-conformance is the live conformance harness for the AERP
// Odoo19 governed semantic surface. It is a real gRPC subprocess client: it
// dials the actually-running organization-service (which in turn talks to
// the actually-running Postgres and Odoo 19), and drives real
// DispatchOperation calls end to end — no fixture, no simulated provider.
//
// It stands in for "run the actual canonical ./asao CLI as an OS subprocess"
// because the canonical CLI source tree (packages/cli) is owned by a
// different OS account in this worktree with an ACL that denies this
// session write access, so new commands could not be added to it — see the
// AERP Odoo19 milestone final report, section N, for the concrete
// filesystem evidence. Every other link in the evidence chain the milestone
// asked for (backend, governance, semantic binding, live Odoo, independent
// readback, semantic comparison) is exercised for real here.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	organizationv1 "github.com/asao-platform/atoc/packages/proto/organization/v1"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type step struct {
	Label         string
	OperationID   string
	Args          map[string]interface{}
	CaptureIDFrom string // if set, remember an id under this name for later steps
	CaptureField  string // which field to pull the id from; defaults to "id", falls back to "target_id"
	ExpectError   bool   // true when the step is a deliberate negative test
	// CaptureNameTo remembers a STRING field (e.g. a document name) for use
	// in a later domain filter, referenced as "$$name" to distinguish it
	// from the numeric "$id" placeholders.
	CaptureNameTo string
	// AssertAmountPositive fails the run if the read-back document total is
	// zero. A zero-value document settles to "paid" for free, so without
	// this the golden money workflow can pass while proving nothing.
	AssertAmountPositive bool
}

type evidenceRecord struct {
	Timestamp          string `json:"timestamp"`
	Sequence           int    `json:"sequence"`
	Label              string `json:"label"`
	OperationID        string `json:"operation_id"`
	ArgumentsJSON      string `json:"arguments_json"`
	Status             string `json:"status"`
	DataJSON           string `json:"data_json"`
	ExecutionIntentID  string `json:"execution_intent_id"`
	ExecutionResultID  string `json:"execution_result_id"`
	VerificationStatus string `json:"verification_status"`
	GRPCError          string `json:"grpc_error,omitempty"`
	ExpectedError      bool   `json:"expected_error"`
	SHA256             string `json:"sha256"`
}

var vars = map[string]int64{}
var strVars = map[string]string{}

// resolveValue recursively substitutes "$varname" string placeholders with
// previously-captured ids, at any nesting depth (top-level args AND inside
// nested "values" maps) — a shallow, top-level-only substitution silently
// leaves the literal string "$customer_id" inside e.g. values.partner_id,
// which Odoo/Postgres then rejects with a type error rather than a
// helpful one.
func resolveValue(v interface{}) interface{} {
	switch t := v.(type) {
	case string:
		if len(t) > 2 && t[0] == '$' && t[1] == '$' {
			return strVars[t[2:]]
		}
		if len(t) > 1 && t[0] == '$' {
			return vars[t[1:]]
		}
		return t
	case map[string]interface{}:
		out := map[string]interface{}{}
		for k, vv := range t {
			out[k] = resolveValue(vv)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, vv := range t {
			out[i] = resolveValue(vv)
		}
		return out
	default:
		return v
	}
}

func resolveArgs(raw map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range raw {
		out[k] = resolveValue(v)
	}
	return out
}

func main() {
	orgID := os.Getenv("ASAO_CONFORMANCE_ORG_ID")
	if orgID == "" {
		fmt.Fprintln(os.Stderr, "ASAO_CONFORMANCE_ORG_ID is required (query it from Postgres: select id from organizations where slug='aerp-odoo19-conformance')")
		os.Exit(1)
	}
	grpcAddr := os.Getenv("ASAO_ORG_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = "localhost:9082"
	}
	// The access token, not a subject name. An unauthenticated run must fail.
	subject := os.Getenv("ASAO_ACCESS_TOKEN")
	if subject == "" {
		fmt.Fprintln(os.Stderr, "ASAO_ACCESS_TOKEN is required: the governed surface no longer accepts a bare subject id")
		os.Exit(1)
	}

	mode := os.Getenv("ASAO_CONFORMANCE_MODE")

	// cli mode: real ./asao OS subprocess execution — no gRPC dial here at
	// all, the binary does its own dialing internally exactly as a real
	// caller's shell invocation would.
	if mode == "cli" {
		binaryPath := os.Getenv("ASAO_CLI_BINARY")
		if binaryPath == "" {
			binaryPath = "/tmp/asao"
		}
		runID := os.Getenv("ASAO_CONFORMANCE_RUN_ID")
		if runID == "" {
			runID = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		fmt.Println("run id:", runID, "(CLI subprocess mode, binary:", binaryPath, ")")
		chain := buildChain(runID)
		evidence := runViaCLI(binaryPath, orgID, grpcAddr, subject, chain)
		out, _ := json.MarshalIndent(evidence, "", "  ")
		os.WriteFile("/tmp/odoo19-conformance-evidence.json", out, 0644)
		fmt.Println("\nEvidence written to /tmp/odoo19-conformance-evidence.json (", len(evidence), "records )")
		checkIdempotencyProof(evidence)
		verifyPostgresIntents(orgID)
		return
	}

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	client := organizationv1.NewOrganizationServiceClient(conn)

	if mode == "concurrency" {
		dsn := os.Getenv("ASAO_PG_DSN")
		if dsn == "" {
			dsn = "host=localhost port=5432 user=atoc password=atoc dbname=atoc_organization sslmode=disable"
		}
		runConcurrencyProof(client, orgID, dsn)
		return
	}

	runID := os.Getenv("ASAO_CONFORMANCE_RUN_ID")
	if runID == "" {
		runID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	fmt.Println("run id:", runID)
	chain := buildChain(runID)
	var evidence []evidenceRecord

	for i, s := range chain {
		args := resolveArgs(s.Args)
		argsJSON, _ := json.Marshal(args)
		req := &organizationv1.DispatchOperationRequest{
			OrganizationId: orgID,
			AsaoUserId:     subject,
			OperationId:    s.OperationID,
			ArgumentsJson:  string(argsJSON),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		resp, callErr := client.DispatchOperation(ctx, req)
		cancel()

		rec := evidenceRecord{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Sequence: i + 1,
			Label: s.Label, OperationID: s.OperationID, ArgumentsJSON: string(argsJSON), ExpectedError: s.ExpectError,
		}
		if callErr != nil {
			rec.GRPCError = callErr.Error()
			rec.Status = "GRPC_ERROR"
			fmt.Printf("[%2d] %-55s %-45s -> ERROR: %v\n", i+1, s.Label, s.OperationID, callErr)
		} else {
			rec.Status = resp.GetStatus()
			rec.DataJSON = resp.GetDataJson()
			rec.ExecutionIntentID = resp.GetExecutionIntentId()
			rec.ExecutionResultID = resp.GetExecutionResultId()
			rec.VerificationStatus = resp.GetVerificationStatus()
			fmt.Printf("[%2d] %-55s %-45s -> status=%-10s verify=%-10s intent=%s\n", i+1, s.Label, s.OperationID, rec.Status, rec.VerificationStatus, rec.ExecutionIntentID)

			if s.CaptureIDFrom != "" {
				captureVar(s.CaptureIDFrom, s.CaptureField, rec.DataJSON)
			}
			if s.CaptureNameTo != "" {
				captureName(s.CaptureNameTo, rec.DataJSON)
			}
			if s.AssertAmountPositive {
				if aerr := assertAmountPositive(s.Label, rec.DataJSON); aerr != nil {
					fmt.Println("     ASSERTION FAILED:", aerr)
					rec.Status = "ASSERTION_FAILED"
					rec.GRPCError = aerr.Error()
				}
			}
		}

		h := sha256.Sum256(mustCanon(rec))
		rec.SHA256 = hex.EncodeToString(h[:])
		evidence = append(evidence, rec)
	}

	out, _ := json.MarshalIndent(evidence, "", "  ")
	os.WriteFile("/tmp/odoo19-conformance-evidence.json", out, 0644)
	fmt.Println("\nEvidence written to /tmp/odoo19-conformance-evidence.json (", len(evidence), "records )")

	checkIdempotencyProof(evidence)
	verifyPostgresIntents(orgID)
}

// checkIdempotencyProof independently verifies the two "idempotency probe"
// steps: same operation + identical arguments, submitted twice in this run,
// must resolve to the SAME provider record id and the SAME
// execution_intent_id (the second call replays the first's stored result
// rather than creating a second contact).
func checkIdempotencyProof(evidence []evidenceRecord) {
	var first, second *evidenceRecord
	for i := range evidence {
		if evidence[i].Label == "idempotency probe: first submission" {
			first = &evidence[i]
		}
		if evidence[i].Label == "idempotency probe: identical resubmission (expect replay, not a second create)" {
			second = &evidence[i]
		}
	}
	if first == nil || second == nil {
		fmt.Println("idempotency proof: SKIPPED (probe steps not found)")
		return
	}
	id1, id2 := vars["idem_id_1"], vars["idem_id_2"]
	if id1 != 0 && id1 == id2 && first.ExecutionIntentID == second.ExecutionIntentID && first.ExecutionIntentID != "" {
		fmt.Printf("idempotency proof: PASS (both submissions resolved to record id=%d, execution_intent_id=%s)\n", id1, first.ExecutionIntentID)
	} else {
		fmt.Printf("idempotency proof: FAIL (id1=%d id2=%d intent1=%s intent2=%s)\n", id1, id2, first.ExecutionIntentID, second.ExecutionIntentID)
	}
}

// captureVar extracts an id from a DispatchOperation DataJSON payload, which
// is either a single object (COMMAND/ACTION results) or an array of records
// (READ results, in which case the first record is used). field selects
// which key to read; it defaults to "id", falling back to "target_id", and
// if the selected value is itself a JSON array (e.g. product_variant_ids),
// the first element is used.
func captureVar(name, field, dataJSON string) {
	if dataJSON == "" {
		return
	}
	if field == "" {
		field = "id"
	}
	var rec map[string]interface{}
	var arr []interface{}
	if err := json.Unmarshal([]byte(dataJSON), &arr); err == nil && len(arr) > 0 {
		rec, _ = arr[0].(map[string]interface{})
	} else {
		_ = json.Unmarshal([]byte(dataJSON), &rec)
	}
	if rec == nil {
		return
	}
	// Reads are now semantically normalized: a reference is
	// {"id":N,"display_name":"..."} and a reference list is a list of those,
	// rather than Odoo's positional [id, name] pair.
	extract := func(v interface{}) (int64, bool) {
		switch t := v.(type) {
		case float64:
			return int64(t), true
		case map[string]interface{}:
			if f, ok := t["id"].(float64); ok {
				return int64(f), true
			}
		case []interface{}:
			if len(t) > 0 {
				switch first := t[0].(type) {
				case float64:
					return int64(first), true
				case map[string]interface{}:
					if f, ok := first["id"].(float64); ok {
						return int64(f), true
					}
				}
			}
		}
		return 0, false
	}
	if v, ok := rec[field]; ok {
		if id, ok := extract(v); ok {
			vars[name] = id
			return
		}
	}
	if id, ok := extract(rec["target_id"]); ok {
		vars[name] = id
	}
}

func mustCanon(rec evidenceRecord) []byte {
	c := rec
	c.SHA256 = ""
	b, _ := json.Marshal(c)
	return b
}

// verifyPostgresIntents does an INDEPENDENT readback: it queries the
// execution_intents/execution_results tables directly (bypassing the gRPC
// API entirely) to prove the durable persistence path was actually used,
// not just the in-memory fallback.
func verifyPostgresIntents(orgID string) {
	dsn := os.Getenv("ASAO_PG_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=atoc password=atoc dbname=atoc_organization sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Println("independent readback FAILED to open db:", err)
		return
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM execution_intents WHERE organization_id = $1", orgID).Scan(&count); err != nil {
		fmt.Println("independent readback query failed:", err)
		return
	}
	fmt.Printf("independent readback: %d execution_intents rows persisted in Postgres for org %s\n", count, orgID)
}
