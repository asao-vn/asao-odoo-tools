package odoo

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Provider dialogs.
//
// An Odoo business method answers a button of the web client. Most act and return nothing, or an
// action showing what they made. Some answer with a dialog instead: an ir.actions.act_window whose
// target is "new". A transfer is only partly done, so Odoo asks whether to create a backorder;
// cancelling time off asks for a reason. Until the dialog is confirmed the button has not done what
// it says.
//
// The executor therefore never reports a dialog as an operation's result. A METHOD_CALL answered
// with one fails with ProviderDialogError, naming the dialog and the operations that complete it. A
// WIZARD operation with a WizardOpenerMethod calls the method and completes the dialog it answers
// with, the way the web client does.
//
// Whether a dialog comes before or after the method acts is read from Odoo's source
// (scripts/dev/odoo-dialog-scan.py, evidence in
// docs/architecture/provider-research/odoo-surface/dialogs-19.0.json) and reviewed into the two
// tables below. A dialog in neither table is reported without claiming either.

// blockingDialogs are dialogs a method answers with before it acts: until one is confirmed, the
// operation has not happened.
var blockingDialogs = map[string]string{
	"confirm.stock.sms":                        "stock.picking.button_validate asks, once per company, before texting the customer",
	"hr.expense.approve.duplicate":             "hr.expense.action_approve asks before approving an expense that duplicates another",
	"hr.expense.post.wizard":                   "hr.expense.action_post asks for the journal and date of an employee-paid expense",
	"hr.expense.refuse.wizard":                 "hr.expense.action_refuse asks for the reason",
	"hr.holidays.cancel.leave":                 "hr.leave.action_cancel asks for the reason",
	"l10n_vn_edi_viettel.cancellation":         "account.move.button_request_cancel asks for the reason and agreement before cancelling an SInvoice",
	"mrp.consumption.warning":                  "mrp.production.button_mark_done asks before consuming other quantities than the bill of materials",
	"mrp.production.backorder":                 "mrp.production.button_mark_done asks whether to create a backorder for what is not produced",
	"mrp.production.serials":                   "mrp.production asks for the serial numbers to produce",
	"payment.capture.wizard":                   "payment.transaction.action_capture asks for the amount when the provider captures partially",
	"pos.close.session.wizard":                 "pos.session closing rolls back and asks how to balance the session's difference",
	"purchase.requisition.alternative.warning": "purchase.order.button_confirm asks what to do with the alternative requests for quotation",
	"stock.backorder.confirmation":             "stock.picking.button_validate asks whether to create a backorder for what is not done",
	"stock.warn.insufficient.qty.scrap":        "stock.scrap.action_validate asks before scrapping more than is in stock",
	"stock.warn.insufficient.qty.unbuild":      "mrp.unbuild.action_validate asks before unbuilding more than is in stock",
	"validate.account.move":                    "account.move.action_post asks before posting an entry with an abnormal amount or date",
}

// followUpDialogs are dialogs a method answers with after it has acted: the operation happened, and
// Odoo offers something more.
var followUpDialogs = map[string]string{
	"account.autopost.bills.wizard": "account.move.action_post posted the bill, then offers to post this vendor's next bills automatically",
}

// Dialog outcomes reported by ProviderDialogError and by an opened wizard's result.
const (
	DialogPerformedNo          = "NO"
	DialogPerformedUnconfirmed = "UNCONFIRMED"
	DialogNotAsked             = "NOT_ASKED"
	DialogCompleted            = "COMPLETED"
)

// providerDialog is a dialog a provider method answered with.
type providerDialog struct {
	Model string
	Name  string
	// ResID is the dialog's record when Odoo created it itself (validate.account.move).
	ResID int64
	// Context is the dialog's context as the provider gave it: an object, or source text.
	Context interface{}
}

// dialogOf reports whether a method's answer is a dialog.
func dialogOf(res interface{}) (providerDialog, bool) {
	action, ok := res.(map[string]interface{})
	if !ok || action["type"] != "ir.actions.act_window" || action["target"] != "new" {
		return providerDialog{}, false
	}
	dialog := providerDialog{Context: action["context"]}
	dialog.Model, _ = action["res_model"].(string)
	dialog.Name, _ = action["name"].(string)
	if id, ok := action["res_id"].(float64); ok && id > 0 {
		dialog.ResID = int64(id)
	}
	return dialog, true
}

func followUpDetail(dialog providerDialog, note string) map[string]interface{} {
	return map[string]interface{}{"model": dialog.Model, "name": dialog.Name, "note": note}
}

// ProviderDialogError is a BUSINESS_PRECONDITION_FAILED: the provider answered an operation with a
// dialog instead of a result. Performed says what is known about the operation: NO when Odoo shows
// that dialog before acting, UNCONFIRMED when the dialog is not one this build has reviewed.
type ProviderDialogError struct {
	Operation     string
	ProviderModel string
	TargetID      int64
	DialogModel   string
	DialogName    string
	Performed     string
	// ResolveWith are the operations that complete this dialog for this model.
	ResolveWith []string
	// Reason is set when the dialog is the one the operation completes but cannot be opened as Odoo
	// gave it.
	Reason string
}

func (e *ProviderDialogError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: Odoo answered %s %d with the dialog %q (%s) instead of a result", e.Operation, e.ProviderModel, e.TargetID, e.DialogName, e.DialogModel)
	if e.Reason != "" {
		fmt.Fprintf(&b, ", which cannot be completed here: %s", e.Reason)
	}
	if e.Performed == DialogPerformedNo {
		b.WriteString("; the operation did not complete")
	} else {
		b.WriteString("; whether Odoo acted before asking is not known, read the record back")
	}
	if len(e.ResolveWith) > 0 {
		fmt.Fprintf(&b, "; complete it with %s", strings.Join(e.ResolveWith, " or "))
	}
	return b.String()
}

func (e *ProviderDialogError) ErrorClass() string {
	return string(ErrClassBusinessPreconditionFailed)
}

func (e *ProviderDialogError) StructuredDetail() map[string]interface{} {
	detail := map[string]interface{}{
		"provider_model": e.ProviderModel,
		"target_id":      e.TargetID,
		"dialog_model":   e.DialogModel,
		"dialog_name":    e.DialogName,
		"performed":      e.Performed,
	}
	if len(e.ResolveWith) > 0 {
		detail["resolve_with"] = append([]string(nil), e.ResolveWith...)
	}
	if e.Reason != "" {
		detail["reason"] = e.Reason
	}
	return detail
}

// DialogNotOpenedError is a BUSINESS_PRECONDITION_FAILED: the method whose dialog an operation
// completes answered without it, and that method does nothing else, so the operation did not happen.
type DialogNotOpenedError struct {
	Operation     string
	ProviderModel string
	TargetID      int64
	Opener        string
	DialogModel   string
}

func (e *DialogNotOpenedError) Error() string {
	return fmt.Sprintf("%s: %s.%s answered %d without the dialog %s this operation completes; the operation did not happen",
		e.Operation, e.ProviderModel, e.Opener, e.TargetID, e.DialogModel)
}

func (e *DialogNotOpenedError) ErrorClass() string {
	return string(ErrClassBusinessPreconditionFailed)
}

func (e *DialogNotOpenedError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"provider_model": e.ProviderModel, "target_id": e.TargetID, "opener": e.Opener,
		"dialog_model": e.DialogModel, "performed": DialogPerformedNo,
	}
}

// dialogError reports a dialog that ends the operation: no result, and what is known about it.
func (a *Adapter) dialogError(entry Odoo19Operation, targetID int64, dialog providerDialog) *ProviderDialogError {
	performed := DialogPerformedUnconfirmed
	if _, before := blockingDialogs[dialog.Model]; before {
		performed = DialogPerformedNo
	}
	return &ProviderDialogError{
		Operation: entry.SemanticID, ProviderModel: entry.ProviderModel, TargetID: targetID,
		DialogModel: dialog.Model, DialogName: dialog.Name, Performed: performed,
		ResolveWith: a.dialogResolvers(entry.ProviderModel, dialog.Model),
	}
}

// dialogResolvers lists the operations this adapter can execute that answer every one of the given
// dialogs on a model: the dialog Odoo asked, after the ones already answered on the way to it.
func (a *Adapter) dialogResolvers(model string, dialogModels ...string) []string {
	var out []string
	consider := func(op Odoo19Operation) {
		if op.ProviderModel != model || op.WizardOpenerMethod == "" {
			return
		}
		for _, dialogModel := range dialogModels {
			if op.WizardModel != dialogModel && op.WizardAlsoAnswers[dialogModel] == "" {
				return
			}
		}
		out = append(out, op.SemanticID)
	}
	for _, op := range CatalogByID {
		consider(op)
	}
	if a.Surface {
		for _, op := range SurfaceCatalogByID {
			consider(op)
		}
	}
	sort.Strings(out)
	return out
}

// dialogContext is the context the web client opens a dialog with: the record whose button answered
// with it (active_model, active_id, active_ids), then the dialog's own context, which wins. A
// context Odoo gives as source text is evaluated for literal values and the active record only;
// anything else is refused rather than guessed.
func dialogContext(model string, targetID int64, dialog providerDialog) (map[string]interface{}, error) {
	out := map[string]interface{}{"active_model": model, "active_id": targetID, "active_ids": []interface{}{targetID}}
	switch given := dialog.Context.(type) {
	case nil:
	case map[string]interface{}:
		for key, value := range given {
			out[key] = value
		}
	case string:
		evaluated, err := evalContextLiteral(given, out)
		if err != nil {
			return nil, err
		}
		for key, value := range evaluated {
			out[key] = value
		}
	default:
		return nil, fmt.Errorf("its context is a %T", given)
	}
	return out, nil
}

// evalContextLiteral evaluates the source text of an action context: a dict of literal values
// (strings, numbers, True, False, None, lists, tuples) and the names active_id, active_ids and
// active_model, read from env.
func evalContextLiteral(source string, env map[string]interface{}) (map[string]interface{}, error) {
	p := &contextLiteral{text: source, env: env}
	value, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.at < len(p.text) {
		return nil, p.fail("unexpected text")
	}
	dict, ok := value.(map[string]interface{})
	if !ok {
		return nil, p.fail("not a dict")
	}
	return dict, nil
}

type contextLiteral struct {
	text string
	at   int
	env  map[string]interface{}
}

func (p *contextLiteral) fail(what string) error {
	return fmt.Errorf("its context %q is not a literal this executor evaluates (%s at %d)", p.text, what, p.at)
}

func (p *contextLiteral) space() {
	for p.at < len(p.text) && strings.ContainsRune(" \t\r\n", rune(p.text[p.at])) {
		p.at++
	}
}

func (p *contextLiteral) value() (interface{}, error) {
	p.space()
	if p.at >= len(p.text) {
		return nil, p.fail("missing value")
	}
	switch c := p.text[p.at]; {
	case c == '{':
		return p.dict()
	case c == '[':
		return p.sequence(']')
	case c == '(':
		return p.sequence(')')
	case c == '\'' || c == '"':
		return p.str()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		return p.name()
	}
	return nil, p.fail("unsupported expression")
}

func (p *contextLiteral) dict() (interface{}, error) {
	p.at++ // {
	out := map[string]interface{}{}
	for {
		p.space()
		if p.at < len(p.text) && p.text[p.at] == '}' {
			p.at++
			return out, nil
		}
		key, err := p.value()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, p.fail("non-string key")
		}
		p.space()
		if p.at >= len(p.text) || p.text[p.at] != ':' {
			return nil, p.fail("missing ':'")
		}
		p.at++
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		out[name] = value
		if !p.separator('}') {
			return nil, p.fail("missing ',' or '}'")
		}
	}
}

func (p *contextLiteral) sequence(closing byte) (interface{}, error) {
	p.at++ // [ or (
	out := []interface{}{}
	for {
		p.space()
		if p.at < len(p.text) && p.text[p.at] == closing {
			p.at++
			return out, nil
		}
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, value)
		if !p.separator(closing) {
			return nil, p.fail("missing separator")
		}
	}
}

// separator consumes a ',' (the closing character is left for the caller's loop) and reports
// whether one of the two follows.
func (p *contextLiteral) separator(closing byte) bool {
	p.space()
	if p.at >= len(p.text) {
		return false
	}
	if p.text[p.at] == ',' {
		p.at++
		return true
	}
	return p.text[p.at] == closing
}

func (p *contextLiteral) str() (interface{}, error) {
	quote := p.text[p.at]
	p.at++
	var b strings.Builder
	for p.at < len(p.text) {
		c := p.text[p.at]
		p.at++
		switch {
		case c == quote:
			return b.String(), nil
		case c == '\\' && p.at < len(p.text):
			escaped := p.text[p.at]
			p.at++
			switch escaped {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(escaped)
			}
		default:
			b.WriteByte(c)
		}
	}
	return nil, p.fail("unterminated string")
}

func (p *contextLiteral) number() (interface{}, error) {
	start := p.at
	p.at++
	for p.at < len(p.text) && strings.ContainsRune("0123456789.eE+-", rune(p.text[p.at])) {
		p.at++
	}
	token := p.text[start:p.at]
	if n, err := strconv.ParseInt(token, 10, 64); err == nil {
		return n, nil
	}
	if f, err := strconv.ParseFloat(token, 64); err == nil {
		return f, nil
	}
	return nil, p.fail("bad number")
}

func (p *contextLiteral) name() (interface{}, error) {
	start := p.at
	for p.at < len(p.text) {
		c := p.text[p.at]
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			break
		}
		p.at++
	}
	token := p.text[start:p.at]
	if p.at < len(p.text) && (p.text[p.at] == '.' || p.text[p.at] == '(' || p.text[p.at] == '[') {
		return nil, p.fail("expression " + token)
	}
	switch token {
	case "True":
		return true, nil
	case "False":
		return false, nil
	case "None":
		return nil, nil
	case "active_id", "active_ids", "active_model":
		return p.env[token], nil
	}
	return nil, p.fail("name " + token)
}
