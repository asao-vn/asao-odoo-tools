package odoo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// resolveRelations validates and, where possible, resolves every
// PROVIDER-field value present in `values` that the catalog entry declares
// as a RelationSpec, generically — one mechanism driven entirely by catalog
// data, not a bespoke per-field handler.
//
// For each such field it:
//  1. accepts an already-numeric id as-is,
//  2. resolves a non-numeric string against RelationSpec.SearchField (exact
//     match) when set, refusing closed if zero or more than one record
//     matches,
//  3. verifies the resulting id actually exists in RelationSpec.TargetModel,
//
// and reports the offending SEMANTIC field name (not target_id, and not a
// raw provider field the caller never typed) on any failure.
func (a *Adapter) resolveRelations(ctx context.Context, entry Odoo19Operation, values map[string]interface{}, providerCtx map[string]interface{}) error {
	if len(entry.Relations) == 0 {
		return nil
	}
	contract := Odoo19Contracts[entry.SemanticID]
	for providerField, rel := range entry.Relations {
		raw, present := values[providerField]
		if !present {
			continue
		}
		semanticName := providerField
		if spec, ok := contract.InputByProviderField(providerField); ok {
			semanticName = spec.Name
		}

		if rel.Multi {
			resolved, err := a.resolveMultiRelation(ctx, entry.SemanticID, semanticName, rel, raw, providerCtx)
			if err != nil {
				return err
			}
			values[providerField] = resolved
			continue
		}

		id, wasNativeNumber, err := a.resolveOneRelation(ctx, entry.SemanticID, semanticName, rel, raw, providerCtx)
		if err != nil {
			return err
		}
		// A value that arrived as a genuine Go numeric type (float64/int/
		// int64 — e.g. an in-process caller that built OperationInput.Values
		// directly, or a JSON number decoded from --values) is left exactly
		// as supplied: the existence check still ran against it, but its
		// representation is already what the provider RPC layer expects.
		// Anything else — a numeric-LOOKING string (the CLI's --department 7
		// arrives this way, since "reference" flags are string-typed so a
		// business name can reach the resolver too) or a resolved name — is
		// always normalized to the resolved int64 id. Skipping that for a
		// numeric string would hand Odoo partner_id:"7" instead of
		// partner_id:7, which the ORM accepts without a type error but
		// computes several server-side defaults (e.g. invoice/shipping
		// address) incorrectly for.
		if !wasNativeNumber {
			values[providerField] = id
		}
	}
	return nil
}

// resolveOneRelation turns one caller-supplied token (a numeric id, a
// numeric-looking string, or — when the relation permits it — a
// business-facing name) into a verified provider id. wasNativeNumber
// reports whether `raw` already arrived as a genuine Go numeric type
// (float64/int/int64) rather than a string — callers use this to decide
// whether the original value's exact representation needs replacing.
func (a *Adapter) resolveOneRelation(ctx context.Context, opID, field string, rel RelationSpec, raw interface{}, providerCtx map[string]interface{}) (id int64, wasNativeNumber bool, err error) {
	switch raw.(type) {
	case float64, int64, int:
		wasNativeNumber = true
	}
	id, isNumeric := coerceRelationID(raw)
	if _, isString := raw.(string); isString && rel.ByName && rel.SearchField != "" {
		isNumeric = false // a code that looks like an id is still a code
	}
	if !isNumeric {
		supplied, isString := raw.(string)
		if !isString || rel.SearchField == "" {
			return 0, false, &RelationNotFoundError{
				Operation: opID, Field: field, Supplied: fmt.Sprintf("%v", raw), TargetModel: rel.TargetModel,
			}
		}
		resolved, err := a.searchRelationByName(ctx, opID, field, rel, supplied, providerCtx)
		if err != nil {
			return 0, false, err
		}
		id = resolved
	}
	count, err := a.Transport.SearchCount(ctx, rel.TargetModel, append([]interface{}{[]interface{}{"id", "=", id}}, rel.Domain...), providerCtx)
	if err != nil {
		return 0, false, err
	}
	if count == 0 {
		return 0, false, &RelationNotFoundError{
			Operation: opID, Field: field, Supplied: fmt.Sprintf("%v", raw), TargetModel: rel.TargetModel,
		}
	}
	return id, wasNativeNumber, nil
}

// resolveMultiRelation handles a many2many field: raw may be a JSON array
// (numbers or names), a comma-separated string, or a single token. Every
// resolved id is verified to exist, exactly like the single-value path, and
// the result is written as Odoo's (6,0,[ids]) replace-all command tuple.
func (a *Adapter) resolveMultiRelation(ctx context.Context, opID, field string, rel RelationSpec, raw interface{}, providerCtx map[string]interface{}) (interface{}, error) {
	tokens, err := splitRelationTokens(raw)
	if err != nil {
		return nil, &RelationNotFoundError{Operation: opID, Field: field, Supplied: fmt.Sprintf("%v", raw), TargetModel: rel.TargetModel}
	}
	ids := make([]interface{}, 0, len(tokens))
	for _, tok := range tokens {
		id, _, err := a.resolveOneRelation(ctx, opID, field, RelationSpec{TargetModel: rel.TargetModel, SearchField: rel.SearchField, Domain: rel.Domain, ByName: rel.ByName}, tok, providerCtx)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return []interface{}{[]interface{}{6, 0, ids}}, nil
}

// splitRelationTokens normalizes the several shapes a multi-relation value
// may arrive in (a JSON array, a comma-separated string, or a bare scalar)
// into one token per referenced record.
func splitRelationTokens(raw interface{}) ([]interface{}, error) {
	switch v := raw.(type) {
	case []interface{}:
		return v, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "[") {
			var arr []interface{}
			if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
				return nil, err
			}
			return arr, nil
		}
		parts := strings.Split(trimmed, ",")
		out := make([]interface{}, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	default:
		return []interface{}{v}, nil
	}
}

// searchRelationByName resolves a business-facing name to an id via an
// exact match against RelationSpec.SearchField, failing closed on zero or
// multiple matches rather than guessing.
func (a *Adapter) searchRelationByName(ctx context.Context, opID, field string, rel RelationSpec, name string, providerCtx map[string]interface{}) (int64, error) {
	recs, err := a.Transport.SearchRead(ctx, rel.TargetModel,
		append([]interface{}{[]interface{}{rel.SearchField, "=", name}}, rel.Domain...),
		[]string{"id", "display_name"}, 5, "", providerCtx)
	if err != nil {
		return 0, err
	}
	if len(recs) == 0 {
		return 0, &RelationNotFoundError{Operation: opID, Field: field, Supplied: name, TargetModel: rel.TargetModel}
	}
	if len(recs) > 1 {
		candidates := make([]map[string]interface{}, 0, len(recs))
		for _, r := range recs {
			candidates = append(candidates, map[string]interface{}{"id": r["id"], "display_name": r["display_name"]})
		}
		return 0, &AmbiguousReferenceError{
			Operation: opID, Field: field, Supplied: name, TargetModel: rel.TargetModel, Candidates: candidates,
		}
	}
	id, _ := coerceRelationID(recs[0]["id"])
	return id, nil
}

// coerceRelationID reports whether raw is already an id (a JSON number, a
// Go int, or a purely numeric string) and returns it as int64.
func coerceRelationID(raw interface{}) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// checkPrecondition reads entry.Precondition.Field off the target record
// and refuses BEFORE a WIZARD or METHOD_CALL strategy contacts the
// provider's mutating call, when it does not match Precondition.Expect.
// entry.Precondition.Field == "" means the operation declares no
// precondition — the check is then a no-op, which is the case for every
// catalog entry except the ones that hand-build a provider side-effect
// whose native entry point would itself have enforced one (see
// finance.payment.register).
func (a *Adapter) checkPrecondition(ctx context.Context, entry Odoo19Operation, targetID int64, providerCtx map[string]interface{}) error {
	if entry.Precondition.Field == "" {
		return nil
	}
	recs, err := a.Transport.Read(ctx, entry.ProviderModel, []int64{targetID}, []string{entry.Precondition.Field}, providerCtx)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return &SingleRecordNotFoundError{Operation: entry.SemanticID, ProviderModel: entry.ProviderModel, TargetID: targetID}
	}
	got := recs[0][entry.Precondition.Field]
	if !valuesMatch(got, entry.Precondition.Expect) {
		return &PreconditionFailedError{
			Operation: entry.SemanticID, ProviderModel: entry.ProviderModel,
			Field: entry.Precondition.Field, Expect: entry.Precondition.Expect, Got: got, TargetID: targetID,
		}
	}
	return nil
}
