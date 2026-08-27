package main

import (
	"encoding/json"
	"fmt"
)

// captureName remembers a string field (document name) from a READ payload.
func captureName(name, dataJSON string) {
	if dataJSON == "" {
		return
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(dataJSON), &arr); err != nil || len(arr) == 0 {
		return
	}
	if v, ok := arr[0]["name"].(string); ok && v != "" {
		strVars[name] = v
	}
}

// assertAmountPositive proves the document being settled actually carries
// money. Odoo marks a zero-total posted document as paid with no payment at
// all, so "payment_state == paid" on its own is not evidence that a
// procurement workflow moved any value.
func assertAmountPositive(label, dataJSON string) error {
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(dataJSON), &arr); err != nil || len(arr) == 0 {
		return fmt.Errorf("%s: no document returned to assert on", label)
	}
	rec := arr[0]
	// the semantic contract renames amount_total -> total_amount
	var total float64
	for _, k := range []string{"total_amount", "amount_total"} {
		if f, ok := rec[k].(float64); ok {
			total = f
			break
		}
	}
	if total <= 0 {
		return fmt.Errorf("%s: document total is %.2f; a zero-value document settles to paid for free and proves nothing", label, total)
	}
	fmt.Printf("     assertion OK: document total is %.2f (non-zero)\n", total)
	return nil
}
