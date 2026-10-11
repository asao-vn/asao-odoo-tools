package direct_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"

	"github.com/asao-platform/atoc/packages/odoo"
)

// odoo19 makes a fake provider answer common.version the way an Odoo 19.0 server does, then hands
// every other request to handler unchanged. The RPC client's series gate
// (packages/odoo/provider_series.go) asks for the version before it logs in and refuses any other
// series, so a fake that expects a login has to say which series it is. Fakes that count every
// contact handle version themselves instead, so the call stays counted.
func odoo19(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var call struct {
			Params struct {
				Service string `json:"service"`
				Method  string `json:"method"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &call) == nil && call.Params.Service == "common" && call.Params.Method == "version" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": map[string]interface{}{"server_version": "19.0-20260803", "server_serie": "19.0"}})
			return
		}
		if answerProviderFit(w, body, odoo.Odoo19ProviderSeries) {
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}
}

// answerProviderFit answers the provider-fit check's reads (DEC-2026-0157) as a stock Odoo of the
// series does: every module of the image installed, no field added by hand. It answers only those
// reads, so a fake that counts its calls still sees exactly the operation's own.
func answerProviderFit(w http.ResponseWriter, body []byte, series string) bool {
	var call struct {
		Params struct {
			Method string        `json:"method"`
			Args   []interface{} `json:"args"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &call) != nil || call.Params.Method != "execute_kw" || len(call.Params.Args) < 7 {
		return false
	}
	model, _ := call.Params.Args[3].(string)
	method, _ := call.Params.Args[4].(string)
	kwargs, _ := call.Params.Args[6].(map[string]interface{})
	switch {
	case model == "ir.module.module" && method == "search_read" && reflect.DeepEqual(kwargs["fields"], []interface{}{"name", "license"}):
		rows := []interface{}{}
		for _, name := range odoo.StandardModules(series) {
			rows = append(rows, map[string]interface{}{"name": name, "license": "LGPL-3"})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": rows})
		return true
	case model == "base.automation" && method == "search_read":
		// The stock image ships base_automation; a stock database runs no automation rule.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": []interface{}{}})
		return true
	case method == "fields_get" && reflect.DeepEqual(kwargs["attributes"], []interface{}{"manual", "required"}):
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": map[string]interface{}{}})
		return true
	}
	return false
}
