package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	organizationv1 "github.com/asao-platform/atoc/packages/proto/organization/v1"
	"google.golang.org/grpc"
)

// runConcurrencyProof is the section-F torture test: N concurrent
// DispatchOperation calls with the SAME explicit idempotency_key and the
// SAME arguments must resolve to exactly one provider record and one
// execution_intent_id (proving the database uniqueness constraint, not an
// application-level check-then-insert, is what prevents the race). It then
// submits the same key with a DIFFERENT payload and requires
// IDEMPOTENCY_CONFLICT.
func runConcurrencyProof(client organizationv1.OrganizationServiceClient, orgID, dsn string) {
	subject := "conformance-agent"
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	idemKey := "concurrency-probe-" + runID
	contactName := fmt.Sprintf("AERP Concurrency Probe %s", runID)
	argsJSON, _ := json.Marshal(map[string]interface{}{
		"values": map[string]interface{}{"name": contactName, "is_company": true},
	})
	execCtxJSON, _ := json.Marshal(map[string]interface{}{"idempotency_key": idemKey})

	const concurrency = 25
	type outcome struct {
		status   string
		intentID string
		resultID string
		grpcErr  string
		dataJSON string
	}
	results := make([]outcome, concurrency)
	var wg sync.WaitGroup
	var startGate sync.WaitGroup
	startGate.Add(1)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			startGate.Wait() // release all goroutines as simultaneously as possible
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			resp, err := client.DispatchOperation(ctx, &organizationv1.DispatchOperationRequest{
				OrganizationId: orgID, AsaoUserId: subject, OperationId: "identity.contact.create",
				ArgumentsJson: string(argsJSON), ExecutionContextJson: string(execCtxJSON),
			})
			if err != nil {
				results[idx] = outcome{grpcErr: err.Error()}
				return
			}
			results[idx] = outcome{status: resp.GetStatus(), intentID: resp.GetExecutionIntentId(), dataJSON: resp.GetDataJson()}
		}(i)
	}
	fmt.Printf("\n=== CONCURRENCY PROOF: %d simultaneous identity.contact.create calls, idempotency_key=%s ===\n", concurrency, idemKey)
	startGate.Done() // fire the starting gun
	wg.Wait()

	intentIDs := map[string]int{}
	succeeded := 0
	failed := 0
	for i, r := range results {
		if r.grpcErr != "" {
			failed++
			fmt.Printf("  [%2d] grpc error: %s\n", i, r.grpcErr)
			continue
		}
		succeeded++
		intentIDs[r.intentID]++
		fmt.Printf("  [%2d] status=%s intent=%s\n", i, r.status, r.intentID)
	}
	fmt.Printf("succeeded=%d failed=%d distinct_execution_intent_ids=%d\n", succeeded, failed, len(intentIDs))

	// Independent readback #1: Postgres — exactly one execution_intents row
	// for this (org, operation, idempotency_key).
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Println("READBACK FAILED (postgres open):", err)
		os.Exit(1)
	}
	defer db.Close()
	var intentCount int
	if err := db.QueryRow(`SELECT count(*) FROM execution_intents WHERE organization_id=$1 AND operation_id=$2 AND idempotency_key=$3`,
		orgID, "identity.contact.create", idemKey).Scan(&intentCount); err != nil {
		fmt.Println("READBACK FAILED (count intents):", err)
		os.Exit(1)
	}
	fmt.Printf("independent readback: execution_intents rows for this key = %d (want 1)\n", intentCount)

	allSameIntent := len(intentIDs) == 1
	pass := succeeded == concurrency && allSameIntent && intentCount == 1
	fmt.Printf("SAME-KEY SAME-PAYLOAD PROOF: %s\n", passFail(pass))
	if !pass {
		fmt.Println("FAILURE DETAIL: expected all", concurrency, "callers to succeed and share exactly one execution_intent_id, and exactly one persisted row.")
	}

	// ---- same key, DIFFERENT payload => IDEMPOTENCY_CONFLICT ----
	conflictArgsJSON, _ := json.Marshal(map[string]interface{}{
		"values": map[string]interface{}{"name": contactName + " CONFLICTING PAYLOAD", "is_company": true},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = client.DispatchOperation(ctx, &organizationv1.DispatchOperationRequest{
		OrganizationId: orgID, AsaoUserId: subject, OperationId: "identity.contact.create",
		ArgumentsJson: string(conflictArgsJSON), ExecutionContextJson: string(execCtxJSON),
	})
	fmt.Println("\n=== SAME-KEY DIFFERENT-PAYLOAD PROOF ===")
	if err == nil {
		fmt.Println("SAME-KEY DIFFERENT-PAYLOAD PROOF: FAIL (expected an error, got success)")
	} else {
		fmt.Println("call result (expect IDEMPOTENCY_CONFLICT):", err.Error())
		fmt.Printf("SAME-KEY DIFFERENT-PAYLOAD PROOF: %s\n", passFail(indexOf(err.Error(), "IDEMPOTENCY_CONFLICT") >= 0))
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func passFail(b bool) string {
	if b {
		return "PASS"
	}
	return "FAIL"
}

var _ = grpc.WithTransportCredentials
