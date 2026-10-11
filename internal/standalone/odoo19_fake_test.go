package standalone_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
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
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}
}
