package standalone

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// resolve finds records and answers their ids, nothing else: what an agent at data level operate needs to
// act on "the customer's latest draft" without being shown the customer (DEC-2026-0182; the 17/10 privacy
// experiment found operate met 2 of 13 goals because it could not find a record by name).
//
// The filter may name only plain fields (names, references, states, dates, amounts, relations): a filter on
// a phone, an e-mail, a note or a file would answer whether a value is there, which is reading it.

const resolveMaxLimit = 20

var resolveOperators = map[string]bool{"=": true, "!=": true, "in": true, "not in": true, "<": true, "<=": true, ">": true,
	">=": true, "ilike": true, "like": true, "=ilike": true}

// checkResolveFilter refuses a condition on a field that is not plain, along every step of a dotted path.
func checkResolveFilter(domain []interface{}) error {
	for _, item := range domain {
		switch term := item.(type) {
		case string:
			if term != "&" && term != "|" && term != "!" {
				return fmt.Errorf("%q is not a domain operator", term)
			}
		case []interface{}:
			if len(term) != 3 {
				return fmt.Errorf("a condition is [field, operator, value]")
			}
			field, _ := term[0].(string)
			operator, _ := term[1].(string)
			if field == "" || !resolveOperators[operator] {
				return fmt.Errorf("condition %v: a field name and one of = != in not-in < <= > >= ilike like", term)
			}
			for _, step := range strings.Split(field, ".") {
				if class := odoo.ClassifyField(step, ""); class != odoo.FieldPlain {
					return fmt.Errorf("%s is %s data: resolve finds records by plain fields only", field, strings.ToLower(string(class)))
				}
			}
		default:
			return fmt.Errorf("a domain holds conditions and operators only")
		}
	}
	return nil
}

func resolveCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:    "resolve",
		Summary: "Find records and answer their ids only: how an agent that may not see the data finds what to act on.",
		Description: `Runs one list operation with your filter and answers the ids of what it found, at most 20, and how many
there were. Nothing else of the records is returned. The filter may name only plain fields (names, references,
states, dates, amounts, relations), never a phone, an e-mail, an address, a tax code, a note or a file, so that
resolve cannot be used to test whether a value is there. It is offered at every data level, operate included.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags: []clicore.Flag{
			personalProfileFlag(), // the Odoo an agent reaches is the person's choice, as on every read
			{Name: "operation", Kind: clicore.KindString, Required: true, Description: "A list operation, e.g. sales.quotation.list or identity.contact.list."},
			{Name: "name", Kind: clicore.KindString, Description: "Records whose name contains this text."},
			{Name: "filter", Kind: clicore.KindJSON, Description: "More conditions on plain fields, as a JSON domain."},
			{Name: "order", Kind: clicore.KindString, Description: "Sort order on plain fields, e.g. 'date_order desc'."},
			{Name: "limit", Kind: clicore.KindInt, Description: "At most this many ids (default and most 20)."},
		},
		Examples: []string{
			config.name + ` resolve --operation identity.contact.list --name "Lê Thành" --output json`,
			config.name + ` resolve --operation sales.quotation.list --filter '[["partner_id.name","ilike","Lê Thành"],["state","=","draft"]]' --order "id desc" --limit 1 --output json`,
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			operationID := strings.TrimSpace(invocation.String("operation"))
			operation, ok := odoo.LookupStandaloneOperation(operationID)
			if !ok || operation.Strategy != odoo.StrategyRead || !strings.HasSuffix(operationID, ".list") {
				return nil, clicore.UsageError("RESOLVE_NOT_A_LIST", "%s is not a list operation", operationID).
					WithRemediation("Name a list operation: catalog list --strategy READ.")
			}
			var domain []interface{}
			if invocation.Has("filter") {
				if err := invocation.JSON("filter", &domain); err != nil {
					return nil, err
				}
			}
			if name := strings.TrimSpace(invocation.String("name")); name != "" {
				domain = append(domain, []interface{}{"name", "ilike", name})
			}
			if err := checkResolveFilter(domain); err != nil {
				return nil, clicore.UsageError("RESOLVE_FILTER_REFUSED", "%v", err)
			}
			order := strings.TrimSpace(invocation.String("order"))
			for _, part := range strings.Split(order, ",") {
				field := strings.Fields(part)
				if len(field) > 0 && odoo.ClassifyField(field[0], "") != odoo.FieldPlain {
					return nil, clicore.UsageError("RESOLVE_FILTER_REFUSED", "%s is not a plain field to sort by", field[0])
				}
			}
			limit, err := invocation.Int("limit", resolveMaxLimit)
			if err != nil {
				return nil, err
			}
			if limit <= 0 || limit > resolveMaxLimit {
				limit = resolveMaxLimit
			}
			executor, err := standaloneTargetExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			outcome, err := executor.Execute(ctx, direct.Request{Operation: operationID, Domain: domain, Limit: limit + 1, Order: order,
				Fields: []string{"id"}})
			if err != nil {
				return nil, classifyExecution(err, operationID, config.surface)
			}
			encoded, _ := json.Marshal(outcome.Data)
			var rows []map[string]interface{}
			if err := json.Unmarshal(encoded, &rows); err != nil {
				return nil, clicore.InternalError(fmt.Errorf("%s answered %T, not a list of records", operationID, outcome.Data))
			}
			ids := make([]int64, 0, len(rows))
			for _, row := range rows {
				if id, ok := row["id"].(float64); ok {
					ids = append(ids, int64(id))
				}
			}
			more := len(ids) > limit
			if more {
				ids = ids[:limit]
			}
			return map[string]interface{}{"operation": operationID, "ids": ids, "count": len(ids), "more": more}, nil
		},
	})
}
