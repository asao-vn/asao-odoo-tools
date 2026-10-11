package odoo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// JSON-2: the external API Odoo 19.0 introduced to replace JSON-RPC (ProtocolJSON2).
//
// One POST per call to /json/2/<model>/<method>. The body names the records (`ids`), the context
// and every argument by its name in the Python signature: the endpoint binds them with
// inspect.signature and refuses positional arguments and unknown names alike. The API key is a
// bearer token and the database travels in X-Odoo-Database, so no login call and no session exist.
// A recordset result comes back as its ids. An error comes back with its HTTP status and the same
// serialized exception (name, message, arguments) JSON-RPC carries, so it classifies the same.
//
// ClientTransport speaks execute_kw's shape (positional args, kwargs); json2Arguments names the
// positional ones. A method the executor calls with positional arguments beyond the records must
// be listed here with Odoo's own parameter names: an unlisted one is refused before anything is
// sent, never guessed.

// json2Signature names a method's positional parameters. For a model method (api.model, or
// create's api.model_create_multi) the first positional argument is the first parameter; for a
// record method it is the record ids.
type json2Signature struct {
	modelMethod bool
	names       []string
}

// json2Signatures are the Odoo 19.0 signatures (odoo/orm/models.py unless noted) of the methods the
// executor calls with positional arguments.
var json2Signatures = map[string]json2Signature{
	"search_read":  {modelMethod: true, names: []string{"domain", "fields", "offset", "limit", "order"}},
	"search_count": {modelMethod: true, names: []string{"domain", "limit"}},
	"search":       {modelMethod: true, names: []string{"domain", "offset", "limit", "order"}},
	"create":       {modelMethod: true, names: []string{"vals_list"}},
	"fields_get":   {modelMethod: true, names: []string{"allfields", "attributes"}},
	"read":         {names: []string{"fields", "load"}},
	"write":        {names: []string{"vals"}},
	"unlink":       {},
	"onchange":     {names: []string{"values", "field_names", "fields_spec"}},
	// purchase/models/purchase_order.py (procurement.bill.create passes an empty list)
	"action_create_invoice": {names: []string{"attachment_ids"}},
}

// json2Arguments turns an execute_kw call into a JSON-2 body.
func json2Arguments(method string, args []interface{}, kwargs map[string]interface{}) (map[string]interface{}, error) {
	body := make(map[string]interface{}, len(args)+len(kwargs))
	for key, value := range kwargs {
		body[key] = value
	}
	signature, known := json2Signatures[method]
	positional := args
	if !signature.modelMethod && len(positional) > 0 {
		// A record method: the first argument is the records.
		if ids, ok := positional[0].([]interface{}); ok {
			if len(ids) > 0 {
				body["ids"] = ids
			}
		} else {
			return nil, fmt.Errorf("odoo json2: %s expects record ids first, got %T", method, positional[0])
		}
		positional = positional[1:]
	}
	if len(positional) > len(signature.names) {
		if !known {
			return nil, fmt.Errorf("odoo json2: JSON-2 takes arguments by name only, and the names of %s's positional arguments are not known here: pass them by name (kwargs)", method)
		}
		return nil, fmt.Errorf("odoo json2: %s takes %d positional arguments, %d given", method, len(signature.names), len(positional))
	}
	for i, value := range positional {
		name := signature.names[i]
		if _, twice := body[name]; twice {
			return nil, fmt.Errorf("odoo json2: %s gets %s both by position and by name", method, name)
		}
		body[name] = value
	}
	return body, nil
}

// executeJSON2 is ExecuteKw over JSON-2. The series gate runs first, so a provider this client may
// not execute against never receives the API key.
func (c *RPCClient) executeJSON2(ctx context.Context, model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error) {
	c.mu.Lock()
	_, err := c.admittedSeriesLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	body, err := json2Arguments(method, args, kwargs)
	if err != nil {
		return nil, err
	}
	return c.guard(c.postJSON2(ctx, model, method, body))
}

func (c *RPCClient) postJSON2(ctx context.Context, model, method string, body map[string]interface{}) (interface{}, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := c.url + "/json/2/" + url.PathEscape(model) + "/" + url.PathEscape(method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "bearer "+c.password)
	if c.db != "" {
		req.Header.Set("X-Odoo-Database", c.db)
	}
	resp, err := c.httpClientFor(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var result interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("failed to decode odoo response: %w", err)
		}
		return result, nil
	}
	var failure struct {
		Name      string        `json:"name"`
		Message   string        `json:"message"`
		Arguments []interface{} `json:"arguments"`
	}
	if json.NewDecoder(resp.Body).Decode(&failure) != nil || (failure.Name == "" && failure.Message == "") {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	detail := failure.Message
	if detail == "" && len(failure.Arguments) > 0 {
		detail, _ = failure.Arguments[0].(string)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// An unknown or expired API key: an access refusal, as a wrong password is over JSON-RPC.
		detail = "Access Denied: " + detail
	}
	return nil, &OdooError{Code: resp.StatusCode, Message: failure.Name, Detail: detail}
}
