package odoo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A provider dialog is a question, not a result. These tests pin the three things the executor
// does with one: a METHOD_CALL answered with a dialog fails and says what is known; a dialog Odoo
// offers after acting is reported next to the success; an opened wizard calls the business method
// the way the web client does and completes the dialog Odoo answers with, in that dialog's context.

// dialogTransport answers CallMethod from a script keyed by "model.method" and records every call.
type dialogTransport struct {
	fakeTransport
	answers  map[string]interface{}
	called   []string
	created  map[string]interface{}
	written  map[string]interface{}
	contexts map[string]map[string]interface{}
}

func newDialogTransport(answers map[string]interface{}) *dialogTransport {
	return &dialogTransport{answers: answers, contexts: map[string]map[string]interface{}{}}
}

func (d *dialogTransport) CallMethod(_ context.Context, model string, _ []int64, method string, _ []interface{}, providerCtx map[string]interface{}) (interface{}, error) {
	key := model + "." + method
	d.called = append(d.called, key)
	d.contexts[key] = providerCtx
	return d.answers[key], nil
}

func (d *dialogTransport) Create(_ context.Context, model string, values map[string]interface{}, providerCtx map[string]interface{}) (int64, error) {
	d.called = append(d.called, model+".create")
	d.contexts[model+".create"] = providerCtx
	d.created = values
	return 55, nil
}

func (d *dialogTransport) Write(_ context.Context, model string, _ []int64, values map[string]interface{}, providerCtx map[string]interface{}) error {
	d.called = append(d.called, model+".write")
	d.written = values
	return nil
}

func dialogAction(model string, context interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "ir.actions.act_window", "target": "new", "res_model": model, "name": "Dialog of " + model, "context": context, "res_id": 0.0}
}

var (
	leaveCancel       = Odoo19Operation{SemanticID: "test.leave.cancel", Strategy: StrategyMethodCall, ProviderModel: "hr.leave", Method: "action_cancel"}
	leaveCancelOpened = Odoo19Operation{
		SemanticID: "test.leave.cancel_opened", Strategy: StrategyWizard, ProviderModel: "hr.leave",
		WizardOpenerMethod: "action_cancel", WizardModel: "hr.holidays.cancel.leave", WizardMethod: "action_cancel_leave",
		WizardAllowedFields: []string{"reason"},
	}
)

func TestAMethodAnsweredWithABlockingDialogIsNotASuccess(t *testing.T) {
	tr := newDialogTransport(map[string]interface{}{
		"hr.leave.action_cancel": dialogAction("hr.holidays.cancel.leave", map[string]interface{}{"default_leave_id": 12.0}),
	})
	res, err := (&Adapter{Transport: tr}).execMethodCall(context.Background(), leaveCancel, OperationInput{TargetID: 12})
	if res != nil {
		t.Fatalf("a dialog was reported as a result: %v", res)
	}
	var dialog *ProviderDialogError
	if !errors.As(err, &dialog) {
		t.Fatalf("err = %v, want a ProviderDialogError", err)
	}
	if dialog.ErrorClass() != string(ErrClassBusinessPreconditionFailed) || dialog.Performed != DialogPerformedNo || dialog.DialogModel != "hr.holidays.cancel.leave" {
		t.Fatalf("dialog error = %+v", dialog)
	}
	if !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("message %q does not say the operation did not complete", err.Error())
	}
}

func TestAnUnreviewedDialogClaimsNeitherOutcome(t *testing.T) {
	tr := newDialogTransport(map[string]interface{}{"hr.leave.action_cancel": dialogAction("some.new.dialog", nil)})
	_, err := (&Adapter{Transport: tr}).execMethodCall(context.Background(), leaveCancel, OperationInput{TargetID: 12})
	var dialog *ProviderDialogError
	if !errors.As(err, &dialog) || dialog.Performed != DialogPerformedUnconfirmed || !strings.Contains(err.Error(), "read the record back") {
		t.Fatalf("err = %v, want an UNCONFIRMED ProviderDialogError telling the caller to read the record back", err)
	}
}

func TestAFollowUpDialogKeepsTheSuccess(t *testing.T) {
	post := Odoo19Operation{SemanticID: "test.bill.post", Strategy: StrategyMethodCall, ProviderModel: "account.move", Method: "action_post"}
	followUp := dialogAction("account.autopost.bills.wizard", nil)
	followUp["res_id"] = 99.0
	tr := newDialogTransport(map[string]interface{}{"account.move.action_post": followUp})
	res, err := (&Adapter{Transport: tr}).execMethodCall(context.Background(), post, OperationInput{TargetID: 8})
	if err != nil {
		t.Fatalf("a dialog Odoo offers after posting failed the post: %v", err)
	}
	out := res.(map[string]interface{})
	if out["created_id"] != int64(8) {
		t.Fatalf("created_id = %v: the follow-up dialog's record was taken for what the operation made", out["created_id"])
	}
	if detail, _ := out["follow_up_dialog"].(map[string]interface{}); detail["model"] != "account.autopost.bills.wizard" {
		t.Fatalf("result %v does not report the follow-up dialog", out)
	}
}

func TestAnOpenedWizardCompletesTheDialogInItsOwnContext(t *testing.T) {
	tr := newDialogTransport(map[string]interface{}{
		"hr.leave.action_cancel":                       dialogAction("hr.holidays.cancel.leave", map[string]interface{}{"default_leave_id": 12.0, "dialog_size": "medium"}),
		"hr.holidays.cancel.leave.onchange":            map[string]interface{}{"value": map[string]interface{}{"reason": false}},
		"hr.holidays.cancel.leave.action_cancel_leave": nil,
	})
	res, _, err := (&Adapter{Transport: tr}).execWizard(context.Background(), leaveCancelOpened, OperationInput{TargetID: 12, Values: map[string]interface{}{"reason": "Doi lich"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(tr.called, ","); got != "hr.leave.action_cancel,hr.holidays.cancel.leave.fields_get,hr.holidays.cancel.leave.onchange,hr.holidays.cancel.leave.create,hr.holidays.cancel.leave.action_cancel_leave" {
		t.Fatalf("calls = %s", got)
	}
	ctx := tr.contexts["hr.holidays.cancel.leave.create"]
	if ctx["default_leave_id"] != 12.0 || ctx["active_model"] != "hr.leave" || ctx["active_id"] != int64(12) {
		t.Fatalf("the dialog was not opened in the context Odoo gave it: %v", ctx)
	}
	if fmt.Sprint(tr.contexts["hr.holidays.cancel.leave.action_cancel_leave"]) != fmt.Sprint(ctx) {
		t.Fatal("the dialog was confirmed in another context than it was opened in")
	}
	if tr.created["reason"] != "Doi lich" || len(tr.created) != 1 {
		t.Fatalf("wizard values = %v, want exactly the caller's reason", tr.created)
	}
	if out := res.(map[string]interface{}); out["dialog"] != DialogCompleted || out["created_id"] != int64(12) {
		t.Fatalf("result = %v", out)
	}
}

func TestAnOpenedWizardWhoseMethodActsWithoutAskingIsDone(t *testing.T) {
	validate := Odoo19Operation{
		SemanticID: "test.transfer.validate_without_backorder", Strategy: StrategyWizard, ProviderModel: "stock.picking",
		WizardOpenerMethod: "button_validate", WizardOpenerActs: true, WizardModel: "stock.backorder.confirmation", WizardMethod: "process_cancel_backorder",
	}
	tr := newDialogTransport(map[string]interface{}{"stock.picking.button_validate": true})
	res, _, err := (&Adapter{Transport: tr}).execWizard(context.Background(), validate, OperationInput{TargetID: 3})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out := res.(map[string]interface{}); out["dialog"] != DialogNotAsked || len(tr.called) != 1 {
		t.Fatalf("result %v after calls %v, want the validation done with no dialog asked", out, tr.called)
	}
}

func TestAnOpenerThatOnlyAsksAndDidNotAskDidNothing(t *testing.T) {
	tr := newDialogTransport(map[string]interface{}{"hr.leave.action_cancel": nil})
	_, _, err := (&Adapter{Transport: tr}).execWizard(context.Background(), leaveCancelOpened, OperationInput{TargetID: 12})
	var notOpened *DialogNotOpenedError
	if !errors.As(err, &notOpened) || notOpened.ErrorClass() != string(ErrClassBusinessPreconditionFailed) || !strings.Contains(err.Error(), "did not happen") {
		t.Fatalf("err = %v, want the cancellation reported as not done", err)
	}
}

func TestAnOpenedWizardRefusesAnotherDialogAndNamesItsResolvers(t *testing.T) {
	validate := Odoo19Operation{
		SemanticID: "test.transfer.validate_with_backorder", Strategy: StrategyWizard, ProviderModel: "stock.picking",
		WizardOpenerMethod: "button_validate", WizardModel: "stock.backorder.confirmation", WizardMethod: "process",
	}
	tr := newDialogTransport(map[string]interface{}{"stock.picking.button_validate": dialogAction("confirm.stock.sms", nil)})
	_, _, err := (&Adapter{Transport: tr}).execWizard(context.Background(), validate, OperationInput{TargetID: 3})
	var dialog *ProviderDialogError
	if !errors.As(err, &dialog) || dialog.DialogModel != "confirm.stock.sms" || dialog.Performed != DialogPerformedNo {
		t.Fatalf("err = %v, want the SMS dialog reported as blocking", err)
	}
	if len(tr.called) != 1 {
		t.Fatalf("calls = %v: a dialog the operation does not complete was opened anyway", tr.called)
	}
}

func TestAnOpenedWizardReusesTheRecordOdooCreated(t *testing.T) {
	post := Odoo19Operation{
		SemanticID: "test.entry.post_abnormal", Strategy: StrategyWizard, ProviderModel: "account.move",
		WizardOpenerMethod: "action_post", WizardModel: "validate.account.move", WizardMethod: "validate_move",
		WizardValues: map[string]interface{}{"ignore_abnormal_amount": true},
	}
	asked := dialogAction("validate.account.move", nil)
	asked["res_id"] = 31.0
	tr := newDialogTransport(map[string]interface{}{"account.move.action_post": asked})
	if _, _, err := (&Adapter{Transport: tr}).execWizard(context.Background(), post, OperationInput{TargetID: 4}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(tr.called, ","); got != "account.move.action_post,validate.account.move.write,validate.account.move.validate_move" {
		t.Fatalf("calls = %s, want Odoo's own dialog record filled in and confirmed", got)
	}
	if tr.written["ignore_abnormal_amount"] != true {
		t.Fatalf("written = %v", tr.written)
	}
}

func TestADialogConfirmationAnsweredWithAnotherDialogIsNotASuccess(t *testing.T) {
	markDone := Odoo19Operation{
		SemanticID: "test.mo.mark_done_despite_consumption", Strategy: StrategyWizard, ProviderModel: "mrp.production",
		WizardOpenerMethod: "button_mark_done", WizardModel: "mrp.consumption.warning", WizardMethod: "action_confirm",
	}
	tr := newDialogTransport(map[string]interface{}{
		"mrp.production.button_mark_done":        dialogAction("mrp.consumption.warning", "{'default_mrp_production_ids': active_ids}"),
		"mrp.consumption.warning.action_confirm": dialogAction("mrp.production.backorder", nil),
	})
	_, _, err := (&Adapter{Transport: tr}).execWizard(context.Background(), markDone, OperationInput{TargetID: 9})
	var dialog *ProviderDialogError
	if !errors.As(err, &dialog) || dialog.DialogModel != "mrp.production.backorder" {
		t.Fatalf("err = %v, want the backorder question that followed reported", err)
	}
	if fmt.Sprint(tr.contexts["mrp.consumption.warning.create"]["default_mrp_production_ids"]) != "[9]" {
		t.Fatalf("a context given as source text was not evaluated: %v", tr.contexts["mrp.consumption.warning.create"])
	}
}

func TestADialogContextIsEvaluatedOnlyForLiterals(t *testing.T) {
	env := map[string]interface{}{"active_model": "hr.expense", "active_id": int64(5), "active_ids": []interface{}{int64(5)}}
	got, err := evalContextLiteral(`{'dialog_size': 'medium', "n": -2, 'x': 1.5, 'ok': True, 'none': None, 'ids': active_ids, 'pair': (1, 'a',), 'nested': {'k': [active_id]},}`, env)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "map[dialog_size:medium ids:[5] n:-2 nested:map[k:[5]] none:<nil> ok:true pair:[1 a] x:1.5]" {
		t.Fatalf("evaluated = %v", got)
	}
	for _, source := range []string{
		`{'a': context.get('x')}`, `{'a': uid}`, `{'a': 1 + 1}`, `{'a': 'open`, `['not', 'a', 'dict']`, `{'a': active_ids[0]}`, `{'a': 1} extra`,
	} {
		if _, err := evalContextLiteral(source, env); err == nil {
			t.Fatalf("%s was evaluated; only literals and the active record are", source)
		}
	}
}

func TestAnOpenedWizardWithAnUnreadableContextRefusesBeforeCreatingAnything(t *testing.T) {
	tr := newDialogTransport(map[string]interface{}{
		"hr.leave.action_cancel": dialogAction("hr.holidays.cancel.leave", "{'default_leave_id': context.get('active_id')}"),
	})
	_, _, err := (&Adapter{Transport: tr}).execWizard(context.Background(), leaveCancelOpened, OperationInput{TargetID: 12})
	var dialog *ProviderDialogError
	if !errors.As(err, &dialog) || dialog.Reason == "" {
		t.Fatalf("err = %v, want a refusal naming why the dialog cannot be completed", err)
	}
	if len(tr.called) != 1 {
		t.Fatalf("calls = %v: something was created for a dialog that cannot be opened faithfully", tr.called)
	}
}

func TestReviewedDialogTablesDoNotOverlap(t *testing.T) {
	for model := range followUpDialogs {
		if _, both := blockingDialogs[model]; both {
			t.Fatalf("%s is reviewed both as asked before acting and as offered after", model)
		}
	}
}

func TestTheWizardOpenerIsPartOfTheDefinitionFingerprint(t *testing.T) {
	op := leaveCancelOpened
	withOpener := OperationDefinitionHash(op)
	op.WizardOpenerMethod = ""
	if OperationDefinitionHash(op) == withOpener {
		t.Fatal("the opener method is not part of the definition evidence is bound to")
	}
	if !strings.HasPrefix(OperationDefinitionHash(op), "sha256:") {
		t.Fatal("fingerprint format changed")
	}
}

func TestAnOpenedWizardAnswersAChainOfDialogs(t *testing.T) {
	markDone := Odoo19Operation{
		SemanticID: "test.mo.mark_done_confirm_consumption_with_backorder", Strategy: StrategyWizard, ProviderModel: "mrp.production",
		WizardOpenerMethod: "button_mark_done", WizardOpenerActs: true,
		WizardModel: "mrp.production.backorder", WizardMethod: "action_backorder",
		WizardAlsoAnswers:   map[string]string{"mrp.consumption.warning": "action_confirm"},
		WizardValues:        map[string]interface{}{"note": "fixed"},
		WizardAllowedFields: []string{},
	}
	tr := newDialogTransport(map[string]interface{}{
		"mrp.production.button_mark_done":           dialogAction("mrp.consumption.warning", map[string]interface{}{"default_mrp_production_ids": []interface{}{9.0}}),
		"mrp.consumption.warning.action_confirm":    dialogAction("mrp.production.backorder", map[string]interface{}{"skip_consumption": true}),
		"mrp.production.backorder.action_backorder": true,
	})
	var creates []map[string]interface{}
	res, _, err := (&Adapter{Transport: &recordingCreates{dialogTransport: tr, creates: &creates}}).execWizard(context.Background(), markDone, OperationInput{TargetID: 9})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "mrp.production.button_mark_done,mrp.consumption.warning.create,mrp.consumption.warning.action_confirm,mrp.production.backorder.create,mrp.production.backorder.action_backorder"
	if got := strings.Join(tr.called, ","); got != want {
		t.Fatalf("calls = %s\nwant    %s", got, want)
	}
	if len(creates) != 2 || len(creates[0]) != 0 || creates[1]["note"] != "fixed" {
		t.Fatalf("created = %v: only the operation's own dialog takes its values", creates)
	}
	if tr.contexts["mrp.production.backorder.create"]["skip_consumption"] != true {
		t.Fatal("the second dialog was not opened in the context the first one's answer gave it")
	}
	out := res.(map[string]interface{})
	if out["dialog"] != DialogCompleted || fmt.Sprint(out["answered_dialogs"]) != "[mrp.consumption.warning mrp.production.backorder]" {
		t.Fatalf("result = %v", out)
	}
}

// recordingCreates keeps every create's values, which dialogTransport overwrites.
type recordingCreates struct {
	*dialogTransport
	creates *[]map[string]interface{}
}

func (r *recordingCreates) Create(ctx context.Context, model string, values map[string]interface{}, providerCtx map[string]interface{}) (int64, error) {
	*r.creates = append(*r.creates, values)
	return r.dialogTransport.Create(ctx, model, values, providerCtx)
}

func TestADialogChainNamesTheOperationsThatAnswerAllOfIt(t *testing.T) {
	surface := &Adapter{Surface: true}
	if got := strings.Join(surface.dialogResolvers("mrp.production", "mrp.consumption.warning", "mrp.production.backorder"), ","); got !=
		"manufacturing.order.mark_done_confirm_consumption_with_backorder,manufacturing.order.mark_done_confirm_consumption_without_backorder" {
		t.Fatalf("resolvers after a confirmed consumption = %s", got)
	}
	if got := surface.dialogResolvers("mrp.production", "mrp.production.backorder"); len(got) != 4 {
		t.Fatalf("resolvers of the backorder question = %v, want the four that answer it", got)
	}
	if got := (&Adapter{}).dialogResolvers("stock.picking", "stock.backorder.confirmation"); len(got) != 0 {
		t.Fatalf("a governed adapter was pointed at surface operations %v", got)
	}
}

// specTransport answers fields_get and onchange like change.production.qty does, and keeps the
// onchange spec it was asked for.
type specTransport struct {
	fakeTransport
	spec map[string]interface{}
}

func (s *specTransport) CallMethod(_ context.Context, _ string, _ []int64, method string, args []interface{}, _ map[string]interface{}) (interface{}, error) {
	switch method {
	case "fields_get":
		return map[string]interface{}{
			"mo_id": map[string]interface{}{"type": "many2one"}, "product_qty": map[string]interface{}{"type": "float"},
			"move_ids": map[string]interface{}{"type": "one2many"},
		}, nil
	case "onchange":
		s.spec, _ = args[2].(map[string]interface{})
		if _, withOrder := s.spec["mo_id"]; !withOrder {
			return map[string]interface{}{"value": map[string]interface{}{"product_qty": 0.0}}, nil
		}
		return map[string]interface{}{"value": map[string]interface{}{"mo_id": 17.0, "product_qty": 7.0}}, nil
	}
	return nil, nil
}

func TestWizardDefaultsAreAskedForTheWholeDialog(t *testing.T) {
	tr := &specTransport{}
	op := Odoo19Operation{SemanticID: "test.mo.change_quantity", WizardModel: "change.production.qty", WizardAllowedFields: []string{"product_qty"}}
	got := (&Adapter{Transport: tr}).wizardOnchangeDefaults(context.Background(), op, nil)
	if fmt.Sprint(got) != "map[product_qty:7]" {
		t.Fatalf("defaults = %v, want the order's quantity and nothing a caller may not set", got)
	}
	if _, asked := tr.spec["move_ids"]; asked || len(tr.spec) != 2 {
		t.Fatalf("onchange spec = %v, want the dialog's plain fields", tr.spec)
	}
}
