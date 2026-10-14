// Package odoo provides the provider-neutral Odoo 19 execution layer for AERP: a
// generic executor with exactly one handler per ExecutionStrategy (READ,
// FIELD_WRITE, METHOD_CALL, WIZARD, DESTRUCTIVE_GUARDED), driven entirely by
// the data-only catalog in this package's odoo19_catalog.go.
//
// Its RPC transport (rpc.go, RPCClient) is a small, self-contained classic
// execute_kw JSON-RPC client, deliberately NOT built on
// internal/infrastructure/odoo.Client, for two independent reasons: (1)
// that package's directory carries a filesystem ACL from a different OS
// account in this worktree, denying this session write access — see the
// AERP milestone final report, section N; and (2) that client's error path
// discards Odoo's detailed exception text (rpcResp.Error.Data), collapsing
// everything to the generic string "Odoo Server Error" — a real defect
// discovered empirically while wiring this milestone's error
// classification, which needs the real text to do anything useful.
// ExecuteKw here forwards arbitrary kwargs — including "context" — straight
// through to Odoo's classic execute_kw method, empirically verified live
// against this worktree's Odoo 19 instance to propagate correctly (unlike
// the frozen LAB CLI's separate, newer JSON-2 transport, whose --context bug
// is a defect in that specific request-construction code, not a limitation
// of Odoo itself).
package odoo

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Transport is the minimal surface the generic executor needs from the
// underlying Odoo RPC client. It exists so the executor can be unit-tested
// against a fake without a live Odoo instance.
type Transport interface {
	SearchRead(ctx context.Context, model string, domain interface{}, fields []string, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error)
	Read(ctx context.Context, model string, ids []int64, fields []string, providerCtx map[string]interface{}) ([]map[string]interface{}, error)
	Write(ctx context.Context, model string, ids []int64, values map[string]interface{}, providerCtx map[string]interface{}) error
	Create(ctx context.Context, model string, values map[string]interface{}, providerCtx map[string]interface{}) (int64, error)
	CallMethod(ctx context.Context, model string, ids []int64, method string, extraArgs []interface{}, providerCtx map[string]interface{}) (interface{}, error)
	SearchCount(ctx context.Context, model string, domain interface{}, providerCtx map[string]interface{}) (int64, error)
	Unlink(ctx context.Context, model string, ids []int64, providerCtx map[string]interface{}) error
}

// PagedSearcher is a transport that can skip records, so a read can page through a model. Reads
// with an offset need it; ClientTransport is one.
type PagedSearcher interface {
	SearchReadPage(ctx context.Context, model string, domain interface{}, fields []string, offset, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error)
}

// GroupReader is a transport that can group and aggregate records on the provider, as Odoo's web
// client does for grouped lists and pivots (formatted_read_group). Grouped reads need it;
// ClientTransport is one.
type GroupReader interface {
	ReadGroup(ctx context.Context, model string, domain interface{}, groupBy, aggregates []string, offset, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error)
}

// KeywordCaller is a transport that can call a record method with keyword arguments, for methods
// whose parameters are keyword-only (message_post). Keyword method calls need it; ClientTransport is
// one.
type KeywordCaller interface {
	CallMethodKwargs(ctx context.Context, model string, ids []int64, method string, kwargs map[string]interface{}, providerCtx map[string]interface{}) (interface{}, error)
}

// ClientTransport adapts *RPCClient to the Transport interface above by
// composing calls to its single generic ExecuteKw primitive.
type ClientTransport struct {
	Client *RPCClient
}

func NewClientTransport(c *RPCClient) *ClientTransport { return &ClientTransport{Client: c} }

func (t *ClientTransport) ProviderVersion(ctx context.Context) (string, error) {
	return t.Client.ProviderVersion(ctx)
}

func idArgs(ids []int64) []interface{} {
	out := make([]interface{}, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// dialect returns the translation for the series this client may execute against, or nil for the
// baseline series. Asking for it runs the series gate, before any credential is sent.
func (t *ClientTransport) dialect(ctx context.Context) (*providerDialect, error) {
	series, err := t.Client.ProviderSeries(ctx)
	if err != nil {
		return nil, err
	}
	return dialectFor(series), nil
}

func (t *ClientTransport) SearchRead(ctx context.Context, model string, domain interface{}, fields []string, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	return t.SearchReadPage(ctx, model, domain, fields, 0, limit, order, providerCtx)
}

// SearchReadPage is SearchRead from the offset-th matching record on.
func (t *ClientTransport) SearchReadPage(ctx context.Context, model string, domain interface{}, fields []string, offset, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	d, err := t.dialect(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.checkModel(model); err != nil {
		return nil, err
	}
	if probed, isProbe := fieldProbeModel(model, domain); isProbe {
		records, err := t.fieldProbe(ctx, probed, domain, fields, offset, limit, order, providerCtx)
		if err != nil || d == nil {
			return records, err
		}
		return d.fieldNamesIn(probed, records), nil
	}
	if d == nil {
		return t.rawSearchRead(ctx, model, domain, fields, offset, limit, order, providerCtx)
	}
	seriesDomain, err := d.domainOut(model, domain)
	if err != nil {
		return nil, err
	}
	seriesOrder, err := d.orderOut(model, order)
	if err != nil {
		return nil, err
	}
	seriesFields, requested := d.fieldsOut(model, fields)
	records, err := t.rawSearchRead(ctx, model, seriesDomain, seriesFields, offset, limit, seriesOrder, providerCtx)
	if err != nil {
		return nil, err
	}
	if err := d.recordsIn(ctx, t, model, records, requested); err != nil {
		return nil, err
	}
	return records, nil
}

// ReadGroup groups and aggregates on the provider through formatted_read_group, the call Odoo's web
// client makes for grouped lists and pivots. Arguments go by name, which both protocols accept. On a
// candidate series the domain, the grouping, the aggregates and the order are translated like a
// read's, and the groups come back in baseline vocabulary.
func (t *ClientTransport) ReadGroup(ctx context.Context, model string, domain interface{}, groupBy, aggregates []string, offset, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	d, err := t.dialect(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.checkModel(model); err != nil {
		return nil, err
	}
	if d != nil {
		if domain, err = d.domainOut(model, domain); err != nil {
			return nil, err
		}
		translate := func(specs []string) ([]string, error) {
			out := make([]string, 0, len(specs))
			for _, spec := range specs {
				series, err := d.groupSpecOut(model, spec)
				if err != nil {
					return nil, err
				}
				out = append(out, series)
			}
			return out, nil
		}
		if groupBy, err = translate(groupBy); err != nil {
			return nil, err
		}
		if aggregates, err = translate(aggregates); err != nil {
			return nil, err
		}
		if order != "" {
			terms, err := translate(strings.Split(order, ","))
			if err != nil {
				return nil, err
			}
			order = strings.Join(terms, ", ")
		}
	}
	kwargs := map[string]interface{}{"domain": domain, "groupby": groupBy, "aggregates": aggregates}
	if offset > 0 {
		kwargs["offset"] = offset
	}
	if limit > 0 {
		kwargs["limit"] = limit
	}
	if order != "" {
		kwargs["order"] = order
	}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	res, err := t.Client.ExecuteKw(ctx, model, "formatted_read_group", []interface{}{}, kwargs)
	if err != nil {
		return nil, err
	}
	groups, err := toRecordSlice(res)
	if err == nil && d != nil {
		d.groupsIn(model, groups)
	}
	return groups, err
}

// CallMethodKwargs calls a record method with its arguments by name, which both protocols accept.
func (t *ClientTransport) CallMethodKwargs(ctx context.Context, model string, ids []int64, method string, kwargs map[string]interface{}, providerCtx map[string]interface{}) (interface{}, error) {
	d, err := t.dialect(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.checkModel(model); err != nil {
		return nil, err
	}
	named := make(map[string]interface{}, len(kwargs)+1)
	for key, value := range kwargs {
		named[key] = value
	}
	if len(providerCtx) > 0 {
		named["context"] = providerCtx
	}
	return t.Client.ExecuteKw(ctx, model, method, []interface{}{idArgs(ids)}, named)
}

// fieldProbe answers which fields a model has: the read execRead makes for OptionalReadFields. It
// reads ir.model.fields, which only the Access Rights group may read. When this login is refused, it
// asks fields_get on the model itself, which any login that may read the model may call, so an
// employee's own login reads as an administrator's does.
func (t *ClientTransport) fieldProbe(ctx context.Context, model string, domain interface{}, fields []string, offset, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	records, err := t.rawSearchRead(ctx, "ir.model.fields", domain, fields, offset, limit, order, providerCtx)
	if err == nil || ClassifyOdooError(err.Error()) != ErrClassAccessDenied {
		return records, err
	}
	described, describeErr := t.Client.ExecuteKw(ctx, model, "fields_get", []interface{}{}, map[string]interface{}{"attributes": []string{"type"}})
	answer, isMap := described.(map[string]interface{})
	if describeErr != nil || !isMap {
		return nil, err
	}
	names := make([]string, 0, len(answer))
	for name := range answer {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]interface{}{"name": name})
	}
	return out, nil
}

func (t *ClientTransport) rawSearchRead(ctx context.Context, model string, domain interface{}, fields []string, offset, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	kwargs := map[string]interface{}{"fields": fields}
	if offset > 0 {
		kwargs["offset"] = offset
	}
	if limit > 0 {
		kwargs["limit"] = limit
	}
	if order != "" {
		kwargs["order"] = order
	}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	res, err := t.Client.ExecuteKw(ctx, model, "search_read", []interface{}{domain}, kwargs)
	if err != nil {
		return nil, err
	}
	return toRecordSlice(res)
}

func (t *ClientTransport) Read(ctx context.Context, model string, ids []int64, fields []string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	d, err := t.dialect(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.checkModel(model); err != nil {
		return nil, err
	}
	seriesFields, requested := d.fieldsOut(model, fields)
	kwargs := map[string]interface{}{"fields": seriesFields}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	res, err := t.Client.ExecuteKw(ctx, model, "read", []interface{}{idArgs(ids)}, kwargs)
	if err != nil {
		return nil, err
	}
	records, err := toRecordSlice(res)
	if err != nil {
		return nil, err
	}
	if err := d.recordsIn(ctx, t, model, records, requested); err != nil {
		return nil, err
	}
	return records, nil
}

func (t *ClientTransport) Write(ctx context.Context, model string, ids []int64, values map[string]interface{}, providerCtx map[string]interface{}) error {
	d, err := t.dialect(ctx)
	if err != nil {
		return err
	}
	if err := d.checkModel(model); err != nil {
		return err
	}
	if values, err = d.valuesOut(writeModeWrite, model, values); err != nil {
		return err
	}
	kwargs := map[string]interface{}{}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	_, err = t.Client.ExecuteKw(ctx, model, "write", []interface{}{idArgs(ids), values}, kwargs)
	return err
}

func (t *ClientTransport) Create(ctx context.Context, model string, values map[string]interface{}, providerCtx map[string]interface{}) (int64, error) {
	d, err := t.dialect(ctx)
	if err != nil {
		return 0, err
	}
	if err := d.checkModel(model); err != nil {
		return 0, err
	}
	if values, err = d.valuesOut(writeModeCreate, model, values); err != nil {
		return 0, err
	}
	kwargs := map[string]interface{}{}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	res, err := t.Client.ExecuteKw(ctx, model, "create", []interface{}{values}, kwargs)
	if err != nil {
		return 0, err
	}
	return toInt64(res)
}

// CallMethod invokes a public provider method. A nil/void Python return
// (e.g. crm.lead.action_set_lost) is reported as (nil, nil) — a valid
// success, not a decode failure. Leading-underscore ("private") methods are
// refused client-side as defense in depth, even though Odoo's own RPC layer
// already hard-blocks them with an AccessError.
func (t *ClientTransport) CallMethod(ctx context.Context, model string, ids []int64, method string, extraArgs []interface{}, providerCtx map[string]interface{}) (interface{}, error) {
	if method == "" || method[0] == '_' {
		return nil, fmt.Errorf("refusing to call %q on %s: leading-underscore/empty method names are never governed ACTION verbs", method, model)
	}
	d, err := t.dialect(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.checkModel(model); err != nil {
		return nil, err
	}
	if err := d.checkMethod(model, method); err != nil {
		return nil, err
	}
	args := append([]interface{}{idArgs(ids)}, extraArgs...)
	kwargs := map[string]interface{}{}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	return t.Client.ExecuteKw(ctx, model, method, args, kwargs)
}

func (t *ClientTransport) SearchCount(ctx context.Context, model string, domain interface{}, providerCtx map[string]interface{}) (int64, error) {
	d, err := t.dialect(ctx)
	if err != nil {
		return 0, err
	}
	if err := d.checkModel(model); err != nil {
		return 0, err
	}
	if domain, err = d.domainOut(model, domain); err != nil {
		return 0, err
	}
	kwargs := map[string]interface{}{}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	res, err := t.Client.ExecuteKw(ctx, model, "search_count", []interface{}{domain}, kwargs)
	if err != nil {
		return 0, err
	}
	return toInt64(res)
}

func (t *ClientTransport) Unlink(ctx context.Context, model string, ids []int64, providerCtx map[string]interface{}) error {
	d, err := t.dialect(ctx)
	if err != nil {
		return err
	}
	if err := d.checkModel(model); err != nil {
		return err
	}
	kwargs := map[string]interface{}{}
	if len(providerCtx) > 0 {
		kwargs["context"] = providerCtx
	}
	_, err = t.Client.ExecuteKw(ctx, model, "unlink", []interface{}{idArgs(ids)}, kwargs)
	return err
}

func toInt64(res interface{}) (int64, error) {
	switch v := res.(type) {
	case float64:
		return int64(v), nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case []interface{}:
		if len(v) > 0 {
			if f, ok := v[0].(float64); ok {
				return int64(f), nil
			}
		}
		return 0, fmt.Errorf("invalid slice for id result: %v", v)
	default:
		return 0, fmt.Errorf("unexpected response type for id result: %T", res)
	}
}

func toRecordSlice(res interface{}) ([]map[string]interface{}, error) {
	slice, ok := res.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response type for record list: %T", res)
	}
	out := make([]map[string]interface{}, 0, len(slice))
	for _, item := range slice {
		rec, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("unexpected record item type: %T", item)
		}
		out = append(out, rec)
	}
	return out, nil
}
