package direct

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ObjectButtons lists the methods a model's own views run as object buttons (`<button type="object"
// name="...">`): what a person can run from Odoo's screens. A pack may call only these
// (DEC-2026-0176). It reads through get_views, which changes nothing.
func (e *Executor) ObjectButtons(ctx context.Context, model string, viewTypes []string) ([]string, error) {
	if e == nil || e.client == nil {
		return nil, errors.New("odoo direct: executor is not initialized")
	}
	model = strings.TrimSpace(model)
	if model == "" || len(viewTypes) == 0 {
		return nil, errors.New("odoo direct: a model and a view type are required")
	}
	views := make([]interface{}, 0, len(viewTypes))
	for _, viewType := range viewTypes {
		views = append(views, []interface{}{false, viewType})
	}
	// A read of the views is a read on any series, as the raw read methods are. get_views is a model
	// method: its arguments go by name, or JSON-2 would take the views for record ids.
	e.client.AdmitCandidateSeries(true)
	defer e.client.AdmitCandidateSeries(false)
	answer, err := e.client.ExecuteKw(ctx, model, "get_views", []interface{}{}, map[string]interface{}{"views": views, "options": map[string]interface{}{}})
	if err != nil {
		return nil, err
	}
	document, _ := answer.(map[string]interface{})
	byType, _ := document["views"].(map[string]interface{})
	found := map[string]bool{}
	for _, viewType := range viewTypes {
		view, _ := byType[viewType].(map[string]interface{})
		arch, _ := view["arch"].(string)
		if arch == "" {
			continue
		}
		names, err := objectButtonsOf(arch)
		if err != nil {
			return nil, fmt.Errorf("odoo direct: the %s view of %s does not parse: %w", viewType, model, err)
		}
		for _, name := range names {
			found[name] = true
		}
	}
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// objectButtonsOf reads a view's arch for its object buttons. Odoo's arch is XML that may hold HTML
// entities (&nbsp;), so the decoder is lenient about them.
func objectButtonsOf(arch string) ([]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(arch))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity
	var names []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "button" {
			continue
		}
		kind, name := "", ""
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "type":
				kind = attr.Value
			case "name":
				name = attr.Value
			}
		}
		if kind == "object" && name != "" && !strings.HasPrefix(name, "_") {
			names = append(names, name)
		}
	}
}
