package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// cliOperationResult mirrors packages/cli/internal/renderer.OperationResult's
// JSON shape closely enough to decode what --output json prints.
type cliOperationResult struct {
	OperationID string          `json:"operationId"`
	Status      string          `json:"status"`
	ScopeRef    string          `json:"scopeRef"`
	Data        json.RawMessage `json:"data"`
}

type cliInnerData struct {
	DataJSON           string `json:"data_json"`
	ExecutionIntentID  string `json:"execution_intent_id"`
	ExecutionResultID  string `json:"execution_result_id"`
	VerificationStatus string `json:"verification_status"`
	Status             string `json:"status"`
}

// flagKeyFor translates an OperationInput JSON key (snake_case, matching
// odoo19exec.OperationInput's json tags) to the CLI flag name
// (kebab-case, matching the shared "schema-odoo19-operation" input schema).
var flagKeyFor = map[string]string{
	"target_id":      "target-id",
	"confirm_impact": "confirm-impact",
	"values":         "values",
	"domain":         "domain",
	"fields":         "fields",
	"limit":          "limit",
	"order":          "order",
}

// buildCLIArgs converts one step's resolved Args map (already had $var
// placeholders substituted by resolveArgs) into ./asao command-line
// arguments: the operation's dotted id becomes its path segments, and each
// argument key becomes its corresponding --flag, JSON-encoding any
// non-scalar value (values/domain/fields) exactly as a real caller typing
// the command by hand would have to.
func buildCLIArgs(operationID string, orgID string, resolved map[string]interface{}) []string {
	args := strings.Split(operationID, ".")
	args = append(args, "--organization", orgID, "--output", "json")

	for k, v := range resolved {
		flagName, ok := flagKeyFor[k]
		if !ok {
			continue
		}
		var strVal string
		switch t := v.(type) {
		case string:
			strVal = t
		case int64:
			strVal = strconv.FormatInt(t, 10)
		case float64:
			strVal = strconv.FormatInt(int64(t), 10)
		case bool:
			strVal = strconv.FormatBool(t)
		default:
			b, _ := json.Marshal(t)
			strVal = string(b)
		}
		if strVal == "" || (k == "target_id" && strVal == "0") {
			continue // target_id 0 means "not set" (create), matches OperationInput's omitempty
		}
		args = append(args, "--"+flagName, strVal)
	}
	return args
}

// runViaCLI executes the exact same chain buildChain produced, but through
// REAL ./asao OS subprocesses instead of a direct gRPC call — this is what
// makes live_cli_executed genuinely true rather than asserted.
func runViaCLI(binaryPath, orgID, grpcAddr, subjectID string, chain []step) []evidenceRecord {
	os.Setenv("ASAO_ORG_GRPC_ADDR", grpcAddr)
	// subjectID is no longer an identity claim: the CLI presents a real
	// identity-service token and the server derives the subject from it.
	os.Setenv("ASAO_ACCESS_TOKEN", subjectID)

	var evidence []evidenceRecord
	for i, s := range chain {
		resolved := resolveArgs(s.Args)
		cliArgs := buildCLIArgs(s.OperationID, orgID, resolved)

		cmd := exec.Command(binaryPath, cliArgs...)
		cmd.Env = os.Environ()
		stdout, err := cmd.Output()

		rec := evidenceRecord{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Sequence: i + 1,
			Label: s.Label, OperationID: s.OperationID,
			ArgumentsJSON: strings.Join(cliArgs, " "), ExpectedError: s.ExpectError,
		}

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				rec.GRPCError = fmt.Sprintf("exit=%d stderr=%s", exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
			} else {
				rec.GRPCError = err.Error()
			}
			// The CLI still prints a JSON body on failure (FAILED status);
			// try to decode it anyway so evidence captures the real error
			// class the backend returned, not just the exit code.
			var res cliOperationResult
			if jsonErr := json.Unmarshal(stdout, &res); jsonErr == nil {
				rec.Status = res.Status
			} else {
				rec.Status = "CLI_ERROR"
			}
			fmt.Printf("[%2d] %-55s %-45s -> ERROR: %s\n", i+1, s.Label, s.OperationID, rec.GRPCError)
		} else {
			var res cliOperationResult
			if jsonErr := json.Unmarshal(stdout, &res); jsonErr != nil {
				rec.Status = "MALFORMED_CLI_OUTPUT"
				rec.GRPCError = jsonErr.Error()
			} else {
				rec.Status = res.Status
				var inner cliInnerData
				if len(res.Data) > 0 {
					_ = json.Unmarshal(res.Data, &inner)
				}
				rec.DataJSON = inner.DataJSON
				rec.ExecutionIntentID = inner.ExecutionIntentID
				rec.ExecutionResultID = inner.ExecutionResultID
				rec.VerificationStatus = inner.VerificationStatus
			}
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
	return evidence
}
