package standalone

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/odoo/review"
)

// The outcome levels of a write (DEC-2026-0183). A provider that answered is not a goal met:
//   ODOO_VALID       the operation ran as Odoo itself runs it (a verified operation, its postcondition
//                    not failed);
//   INTENT_VERIFIED  the record holds what was asked: every value the caller set, read back from Odoo;
//   RESULT_ACCEPTED  nothing in the books around it says it is wrong: the review checks over the record's
//                    model run on the weeks around its date, and no finding names it.
// Each level holds only when the one before does. --accept asks for them on any write.

// Outcome levels.
const (
	LevelNotValid       = "NOT_VALID"
	LevelOdooValid      = "ODOO_VALID"
	LevelIntentVerified = "INTENT_VERIFIED"
	LevelResultAccepted = "RESULT_ACCEPTED"
)

type acceptance struct {
	Level string `json:"level"`
	// Model and RecordID name the record judged.
	Model    string `json:"model,omitempty"`
	RecordID int64  `json:"recordId,omitempty"`
	// Intent lists each value asked for and what Odoo holds; Mismatched names the ones that differ.
	Intent     []intentValue `json:"intent,omitempty"`
	Mismatched []string      `json:"mismatched,omitempty"`
	// Checks are the review checks run for acceptance, Findings the ones that name the record.
	Checks   []string         `json:"checks,omitempty"`
	Findings []review.Finding `json:"findings,omitempty"`
	// Why says what stopped it at its level, when it stopped below RESULT_ACCEPTED.
	Why string `json:"why,omitempty"`
}

type intentValue struct {
	Field    string      `json:"field"`
	Asked    interface{} `json:"asked"`
	Recorded interface{} `json:"recorded"`
	Holds    bool        `json:"holds"`
}

type acceptedOutcome struct {
	direct.Outcome
	Acceptance *acceptance `json:"acceptance"`
}

// acceptedModels is, for each model a review reads, the list operation its checks read it through and
// the date that places a record in the books.
var acceptedModels = map[string]struct{ operation, date string }{
	"account.move":    {"finance.invoice.list", "invoice_date"},
	"account.payment": {"finance.payment.list", "date"},
	"hr.expense":      {"people.expense.list", "date"},
	"purchase.order":  {"procurement.rfq.list", "date_order"},
}

func acceptFlag() clicore.Flag {
	return clicore.Flag{Name: "accept", Kind: clicore.KindBool,
		Description: "After the write, judge its outcome: ODOO_VALID, INTENT_VERIFIED (every value asked for is what Odoo holds) and RESULT_ACCEPTED (no review check finds a problem with the record)."}
}

func judgeOutcome(ctx context.Context, executor *direct.Executor, op direct.Operation, request direct.Request, outcome direct.Outcome, config programConfig) *acceptance {
	result := &acceptance{Level: LevelNotValid, Model: op.ProviderModel}
	switch {
	case outcome.Status != "SUCCESS":
		result.Why = "Odoo did not answer success: " + outcome.Status
		return result
	case outcome.Verification == "FAILED":
		result.Why = "the operation's own postcondition failed"
		return result
	case outcome.Readiness != "CURRENT_VERIFIED":
		result.Why = "the operation is " + outcome.Readiness + " on this Odoo: it is not known to run as Odoo itself does"
		return result
	}
	result.Level = LevelOdooValid
	id := request.ProviderTargetID
	if id == 0 {
		id, _ = strconv.ParseInt(outcome.ProviderReference, 10, 64)
	}
	if id == 0 || op.ProviderModel == "" {
		result.Why = "the write names no record to read back"
		return result
	}
	result.RecordID = id
	// Intent: each value asked for, read back as Odoo holds it.
	fields := map[string]string{}
	for _, input := range op.Inputs {
		fields[input.Name] = input.ProviderField
	}
	var providerFields []string
	for name := range request.Values {
		if field := fields[name]; field != "" {
			providerFields = append(providerFields, field)
		}
	}
	model, _ := acceptedModels[op.ProviderModel]
	if model.date != "" {
		providerFields = append(providerFields, model.date, "state")
	}
	read, err := executor.Call(ctx, direct.RawRequest{Model: op.ProviderModel, Method: "read",
		Args: []interface{}{[]interface{}{id}}, Kwargs: map[string]interface{}{"fields": providerFields}}, false)
	rows, _ := read.Data.([]interface{})
	if err != nil || len(rows) != 1 {
		result.Why = fmt.Sprintf("the record %s %d could not be read back", op.ProviderModel, id)
		return result
	}
	record, _ := rows[0].(map[string]interface{})
	for name, asked := range request.Values {
		field := fields[name]
		if field == "" {
			continue
		}
		holds := sameValue(asked, record[field])
		result.Intent = append(result.Intent, intentValue{Field: name, Asked: asked, Recorded: record[field], Holds: holds})
		if !holds {
			result.Mismatched = append(result.Mismatched, name)
		}
	}
	if len(result.Mismatched) > 0 {
		result.Why = "Odoo holds other values than were asked for: " + strings.Join(result.Mismatched, ", ")
		return result
	}
	result.Level = LevelIntentVerified
	// Acceptance: the review checks that read this model, on the 31 days up to the record's date.
	if model.operation == "" {
		result.Why = "no review check reads " + op.ProviderModel + ": acceptance does not apply"
		return result
	}
	day, ok := parseRecordDay(record[model.date])
	if !ok {
		result.Why = "the record has no " + model.date + " yet (a draft is accepted when it is posted)"
		return result
	}
	if state, _ := record["state"].(string); state == "draft" {
		result.Why = "the record is a draft: the review reads posted records, so it is judged when it is posted"
		return result
	}
	for _, check := range review.Catalog() {
		for _, need := range check.Needs {
			if need == model.operation {
				result.Checks = append(result.Checks, check.ID)
				break
			}
		}
	}
	reader := &catalogReader{executor: executor, config: config}
	report, err := review.Run(ctx, reader, review.Options{From: day.AddDate(0, 0, -31), To: day, Checks: result.Checks})
	if err != nil {
		result.Why = "the review checks could not run: " + err.Error()
		return result
	}
	for _, finding := range report.Findings {
		for _, ev := range finding.Evidence {
			if ev.ID == id && ev.Operation == model.operation {
				result.Findings = append(result.Findings, finding)
				break
			}
		}
	}
	if len(result.Findings) > 0 {
		result.Why = fmt.Sprintf("%d review finding(s) name this record: %s", len(result.Findings), result.Findings[0].Title)
		return result
	}
	result.Level = LevelResultAccepted
	return result
}

func parseRecordDay(value interface{}) (time.Time, bool) {
	text, _ := value.(string)
	if len(text) < 10 {
		return time.Time{}, false
	}
	day, err := time.Parse("2006-01-02", text[:10])
	return day, err == nil
}

// sameValue compares a value asked for with what Odoo holds: a relation by its id, or by its name when
// a name was asked for; numbers as numbers; text as text.
func sameValue(asked, recorded interface{}) bool {
	if pair, ok := recorded.([]interface{}); ok && len(pair) == 2 {
		if id, ok := asNumber(asked); ok {
			recordedID, _ := asNumber(pair[0])
			return id == recordedID
		}
		name, _ := pair[1].(string)
		return strings.Contains(strings.ToLower(name), strings.ToLower(fmt.Sprint(asked)))
	}
	if a, ok := asNumber(asked); ok {
		r, ok := asNumber(recorded)
		return ok && a == r
	}
	if b, ok := asked.(bool); ok {
		return recorded == b
	}
	return fmt.Sprint(asked) == fmt.Sprint(recorded)
}

func asNumber(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n, err == nil
	}
	return 0, false
}
