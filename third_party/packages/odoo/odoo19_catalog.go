package odoo

import "strings"

// ExecutionStrategy is the provider-shape a semantic operation is executed
// through. Every Odoo19Operation declares exactly one strategy, and the
// adapter has exactly one generic handler per strategy — never one handler
// per operation. See adapter.go and strategies.go.
type ExecutionStrategy string

const (
	StrategyRead               ExecutionStrategy = "READ"
	StrategyFieldWrite         ExecutionStrategy = "FIELD_WRITE"
	StrategyMethodCall         ExecutionStrategy = "METHOD_CALL"
	StrategyWizard             ExecutionStrategy = "WIZARD"
	StrategyDestructiveGuarded ExecutionStrategy = "DESTRUCTIVE_GUARDED"
	// StrategyModuleInstall installs one provider module by calling button_immediate_install on a targeted ir.module.module; ConfirmImpact mandatory; only admin.module.install may use it
	StrategyModuleInstall ExecutionStrategy = "module_install"
	// StrategyReport renders one of the target record's PDF reports (ReportName), as the web client's
	// Print menu does: through a logged-in web session, since Odoo serves report PDFs to no API key
	// and its render method is private (rpc_report.go). It changes nothing.
	StrategyReport ExecutionStrategy = "REPORT"
)

// SemanticKind mirrors the READ / COMMAND / ACTION taxonomy used in the
// AERP semantic candidate research. The originally cited working file
// (odoo19-aerp-semantic-candidates.json) is not part of this repository;
// see docs/architecture/provider-research/odoo19/ODOO_58_PROVENANCE_MATRIX.md
// and docs/architecture/provider-research/odoo19/RESTORATION_RECORD.md.
type SemanticKind string

const (
	KindRead    SemanticKind = "READ"
	KindCommand SemanticKind = "COMMAND"
	KindAction  SemanticKind = "ACTION"
)

// WriteMode declares, as data, whether a FIELD_WRITE operation is allowed to
// create a record, to update an existing one, or both.
//
// Without it the executor inferred intent purely from "was a target id
// supplied?", so `people.employee.update` with no target id silently fell
// through to a create — it reported success for a record the caller never
// asked to exist. Intent now comes from the catalog rather than from the
// shape of the arguments, and the mismatch is refused before the provider is
// contacted.
type WriteMode string

const (
	// WriteModeCreate: the operation only ever creates. A target id is a
	// caller error, because the operation cannot update anything.
	WriteModeCreate WriteMode = "CREATE"
	// WriteModeUpdate: the operation only ever updates. A missing target id
	// is a caller error and must never degrade into a create.
	WriteModeUpdate WriteMode = "UPDATE"
	// WriteModeUpsert: the operation legitimately does both, and the target
	// id is what selects between them. Only for operations whose semantics
	// really are "ensure this record is in this state" (work.milestone.reach
	// creates a milestone, then marks the same milestone reached).
	WriteModeUpsert WriteMode = "UPSERT"
)

// VerificationSpec describes how to prove a mutation actually happened by
// reading the target record back from the provider after execution. This is
// empirical-evidence-driven: every field/expected pair here was observed
// live during the odoo19 research pass, not guessed from method names.
type VerificationSpec struct {
	Field        string      // provider field to read back
	Expect       interface{} // exact expected value ("" or nil = only assert field is readable/changed)
	ExpectChange bool        // if true, only assert the field changed vs BeforeValue captured pre-execution
}

// RelationSpec declares that a provider field is a many2one (or many2many,
// if Multi) reference into another provider model, and how the executor may
// resolve a caller-supplied value into a concrete id BEFORE the provider is
// contacted.
//
// This is what closes two related gaps at once: (1) a numeric id the
// caller got wrong (a typo, a stale id, a guess) is validated against
// TargetModel and rejected by the SEMANTIC field name — never blamed on
// target_id, never left to degrade into an opaque provider error — and
// (2) when SearchField is set, a caller may supply a business-facing name
// ("Engineering", "In Progress") instead of an opaque integer, resolved by
// exact match against that field.
type RelationSpec struct {
	// TargetModel is the provider model the referenced id must exist in.
	TargetModel string
	// SearchField, if set, is matched exactly against a caller-supplied
	// value that is not already numeric, to resolve a business-facing name
	// to an id. Empty disables name resolution — only a numeric id (or
	// numeric string) is accepted for that field.
	SearchField string
	// Multi marks a many2many field: the resolved id(s) are written as the
	// Odoo (6,0,[ids]) replace-all command tuple. The caller may supply a
	// JSON array, a comma-separated list, or a single value.
	Multi bool
	// Domain holds the referenced record to a subset (a journal entry line's move is a draft journal
	// entry; an attachment's record is one the operation's entity reaches): the id must match it too.
	// Omitted from the definition when empty, so the definitions recorded before it keep their hash.
	Domain []interface{} `json:",omitempty"`
	// ByName makes a string always a name for SearchField, a number being the only way to give an id:
	// an account code (6427) looks like an id, and taking it for one would post to another account.
	ByName bool `json:",omitempty"`
}

// LineSpec is a create field that holds lines, such as a journal entry's line_ids: the fields each
// line accepts, their typing and their relations. Each line is checked and resolved on its own, then
// written as Odoo's (0, 0, {...}) command.
type LineSpec struct {
	AllowedFields []string
	InputTypes    map[string]string
	Relations     map[string]RelationSpec
}

// ImpactSpec is one dependent-record probe for a DESTRUCTIVE_GUARDED
// operation: Field on Model must equal the target id for a record to count
// as a dependent.
type ImpactSpec struct {
	Model string
	Field string
}

// Odoo19Operation is one row of the semantic catalog: pure data. The
// generic executor dispatches on Strategy and never contains business logic
// specific to a single SemanticID.
type Odoo19Operation struct {
	SemanticID         string
	Kind               SemanticKind
	Strategy           ExecutionStrategy
	RequiredCapability Capability
	Risk               RiskClassification
	// Confidence values: LIVE_WORKFLOW_VERIFIED / LIVE_READ_VERIFIED / SOURCE_DISCOVERED.
	// Originally cited odoo19-*.json working files are not in this repository; see
	// docs/architecture/provider-research/odoo19/ODOO_58_PROVENANCE_MATRIX.md and
	// artifacts/aerp-odoo19-conformance/evidence.json.
	Confidence string
	// Applicability is catalog metadata only. It expresses the semantic
	// surface each operation belongs to; it does not activate a runtime.
	Applicability   SurfaceApplicability
	RequiredModules []string

	ProviderModel string

	// READ
	ReadFields []string
	// OptionalReadFields are read only when the provider actually declares them. Odoo 19 makes
	// module `stock` optional, so fields it contributes (picking_ids, is_storable) must not be
	// requested unconditionally: a read against an Odoo without that module would fail. V2_16 F5.
	OptionalReadFields []string
	DefaultSort        string
	// ReadNewRecord reads the values of a new record as Odoo computes them when a form opens on one
	// (onchange with nothing set): defaults, related values and what the model supplies itself. It is
	// how the Settings page shows res.config.settings, whose stored records are only past saves. The
	// read answers one record and takes no target or domain.
	ReadNewRecord bool
	// ProviderDomain is a fixed provider-side domain ANDed into every read this
	// operation performs, including a TargetID lookup. It exists so a semantic
	// operation can constrain a shared provider model to the business subset it
	// actually means (e.g. account.move.line holds both invoice lines and pure
	// journal-entry lines). Empty means no constraint, which is what all
	// pre-existing operations declare, so their behaviour is unchanged.
	ProviderDomain []interface{}

	// FIELD_WRITE. WriteMode says whether this operation creates, updates or
	// does both; the executor refuses a target id that contradicts it instead
	// of quietly switching between create and update.
	WriteMode     WriteMode
	AllowedFields []string
	// ForcedValues are provider-field values this operation ALWAYS writes,
	// regardless of what the caller supplied. This is how a semantic verb
	// that means one specific provider state change stops requiring the
	// caller to know the raw enum: `crm.opportunity.convert` forces
	// type=opportunity itself, so the caller writes `convert --target-id N`
	// and never learns that Odoo models the conversion as a selection field.
	// Forced values win over caller input and are applied after remapping.
	ForcedValues map[string]interface{}
	// FieldAliasesJSON declares stable-semantic-field -> provider-field
	// remaps as a JSON object string, e.g. {"manager_id":"parent_id"}. This
	// is how the adapter hides provider naming quirks (Odoo's employee
	// "manager" field is parent_id, not manager_id) from callers. Empty
	// string means no remapping is needed.
	FieldAliasesJSON string

	// METHOD_CALL — public, no-arg-or-primitive-only methods ONLY. Never a
	// method whose Python signature expects an ORM recordset positional arg
	// (empirically unsafe over RPC — see crm.lead.convert_opportunity in the
	// research corpus).
	Method     string
	MethodArgs []interface{} // static extra positional args, e.g. [] for attachment_ids
	// MethodKwargs and MethodInputs call a method that takes keyword arguments (message_post,
	// activity_schedule): MethodKwargs are fixed by the operation and always win (a note stays an
	// internal note), MethodInputs are the caller values it accepts as keywords, typed by InputTypes and
	// resolved through Relations; MethodRequiredInputs must be given.
	MethodKwargs         map[string]interface{}
	MethodInputs         []string
	MethodRequiredInputs []string
	// ReportName is the report a REPORT operation renders (sale.report_saleorder).
	ReportName string

	// WIZARD
	WizardModel     string // transient model, e.g. sale.advance.payment.inv
	WizardLinkField string // field on the wizard set to the target record id(s), e.g. "sale_order_ids" or "line_ids"
	WizardLinkMulti bool   // true => value is [(6,0,[ids])], false => value is a single id
	WizardMethod    string // method invoked on the created wizard record
	WizardValues    map[string]interface{}
	// WizardActiveContext opens the wizard the way Odoo's own button does: with active_model,
	// active_id and active_ids naming the target in the provider context, which is where most
	// wizards' default_get finds their record. Empty WizardLinkField is then allowed.
	WizardActiveContext bool
	// WizardStandalone is a wizard no record opens, such as the Settings page (res.config.settings
	// and its execute button): it takes no target and no active context. The caller's values make
	// the wizard, its other fields take what Odoo shows on opening it, and WizardMethod applies it.
	WizardStandalone bool
	// WizardAllowedFields are the wizard fields a caller may supply (a reason, a date, a quantity).
	// Empty keeps the earlier behaviour: caller values are not passed to the wizard.
	WizardAllowedFields []string
	// WizardContext and WizardContextTarget are the context the opening button and its action add,
	// read from Odoo's own definitions: constant keys, and keys naming the target record ("id" for
	// the id, "ids" for a one-element list), e.g. crm.lead.lost's default_lead_ids: active_ids.
	WizardContext       map[string]interface{}
	WizardContextTarget map[string]string
	// WizardOpenerMethod is the business method whose answer is the wizard (provider_dialog.go): the
	// operation calls it as the web client's button does, then opens the dialog it answers with in the
	// context Odoo gave that dialog and confirms it with WizardMethod. WizardActiveContext,
	// WizardContext and WizardContextTarget do not apply: the dialog brings its own context.
	WizardOpenerMethod string
	// WizardOpenerActs says the opener does the operation itself when it has nothing to ask (a
	// transfer with nothing left over is validated, no backorder question). Otherwise the opener
	// only ever opens the dialog, and an answer without it means the operation did not happen.
	WizardOpenerActs bool
	// WizardAlsoAnswers are the other dialogs the operation answers on its way, where Odoo asks in a
	// chain (dialog model -> the button pressed): a manufacturing order whose consumption differs from
	// its bill of materials asks to confirm that, then whether to create a backorder. Only the
	// operation's own dialog (WizardModel) takes values.
	WizardAlsoAnswers map[string]string

	// InputTypes and InputEnums type the provider-native inputs of an operation that has no
	// semantic contract (the surface catalog): the CLI type of each accepted field (boolean,
	// integer, number, reference or string) and the closed value set of a selection field. Without
	// them every such input would be a string, and a boolean field given "false" would be stored as
	// true by the provider.
	InputTypes map[string]string
	InputEnums map[string][]string
	// WriteContext is the provider context a FIELD_WRITE always runs with, over the caller's: an
	// inventory count writes in inventory_mode, so it sets the counted quantity of the quant it counts.
	WriteContext map[string]interface{}
	// LineInputs are the create fields that hold lines (LineSpec).
	LineInputs map[string]LineSpec
	// InputPrefixes hold a provider-native input to values that start with a prefix, so an
	// operation whose provider domain scopes records by that field's value cannot write the field
	// outside the scope either (website.media: a url under /media/). A path prefix (one starting
	// with "/") also requires a clean path, without "." or ".." segments or doubled slashes. The
	// value is refused before the provider is contacted.
	InputPrefixes map[string]string
	// When the wizard's link field does not accept the target id directly
	// (e.g. account.payment.register.line_ids wants account.move.line ids,
	// not the account.move id itself), the executor resolves the real link
	// ids generically by querying WizardLinkSourceModel for records where
	// WizardLinkSourceDomainField equals the target id, ANDed with
	// WizardLinkSourceExtraDomain. Empty WizardLinkSourceModel means: use
	// the target id directly (no resolution needed).
	WizardLinkSourceModel       string
	WizardLinkSourceDomainField string
	WizardLinkSourceExtraDomain [][]interface{}

	// DESTRUCTIVE_GUARDED
	ImpactModel string // model to probe for dependents before allowing delete
	ImpactField string // FK field on ImpactModel pointing back at the target id
	// AdditionalImpacts are extra dependent-record checks beyond the primary
	// ImpactModel/ImpactField, probed the same way and folded into the same
	// aggregate count/sample. work.project.delete's own KnownSideEffects
	// note that Odoo cascade-deletes BOTH tasks and milestones — declaring
	// only project.task here checked one of the two the operation's own
	// documentation claims to guard, so a project with zero tasks but a
	// live milestone reported dependent_count:0 and deleted the milestone
	// unconfirmed. A relation the provider itself cascades on belongs here
	// even when it is not the primary one the operation is named after.
	AdditionalImpacts []ImpactSpec

	// Relations declares, per PROVIDER field name present in this
	// operation's writable values, what provider model that field
	// references and (optionally) how to resolve a name to an id. Applied
	// generically by the FIELD_WRITE strategy to every operation that
	// declares it — never a per-operation bespoke handler.
	Relations map[string]RelationSpec

	// Precondition, when Field is non-empty, is read from ProviderModel and
	// compared BEFORE a FIELD_WRITE update, WIZARD, or METHOD_CALL strategy
	// contacts the provider's mutating call. This is what stops a semantic
	// operation from re-implementing a provider action's own effect while
	// skipping the invariant that action enforces (e.g. Odoo refuses to
	// register a payment against a draft account.move; a hand-built wizard
	// call that bypasses action_register_payment does not inherit that
	// check for free — it must be declared here).
	Precondition VerificationSpec

	Verification VerificationSpec
	// AdditionalVerifications are extra postconditions that must ALSO hold
	// for the operation to be reported VERIFIED, checked in the same
	// readback call as Verification. A single field readback (e.g.
	// "payment_state == paid") can be trivially true on a record that was
	// never supposed to reach that state at all; this is how a mutation's
	// reported success is tied to more than one downstream field.
	AdditionalVerifications []VerificationSpec
	KnownSideEffects        []string

	// Postcondition metadata for Provider-as-Code MANAGE-mode mutation
	// support. If any field below is empty for a mutating operation, that
	// operation is UNSUPPORTED for Provider-as-Code MANAGE-mode mutation;
	// direct/manual execution via asao aerp / asao odoo is unaffected.
	PostconditionVerifyOperation  string
	PostconditionComparison       string
	PostconditionObservedPath     string
	PostconditionExpectedSource   string
	PostconditionExpectedField    string
	PostconditionExpectedConstant string
}

// invoiceDocumentDomain constrains account.move to the invoice/refund/receipt document
// subset. account.move also holds pure journal entries (move_type 'entry'), which are not
// invoices and must never be reachable through a finance.invoice.* operation.
var invoiceDocumentDomain = []interface{}{
	[]interface{}{"move_type", "in", []interface{}{"out_invoice", "out_refund", "in_invoice", "in_refund", "out_receipt", "in_receipt"}},
}

// invoiceLineDocumentDomain is the same subset expressed from account.move.line.
var invoiceLineDocumentDomain = []interface{}{
	[]interface{}{"move_id.move_type", "in", []interface{}{"out_invoice", "out_refund", "in_invoice", "in_refund", "out_receipt", "in_receipt"}},
}

// opportunityDomain constrains crm.lead to the opportunity subtype.
var opportunityDomain = []interface{}{[]interface{}{"type", "=", "opportunity"}}

// leadInputDomain constrains crm.lead to the lead subtype. crm.opportunity.convert's
// INPUT is a lead — it forces type=opportunity as its whole purpose — so the subtype it
// must enforce is the one it accepts, not the one it produces.
var leadInputDomain = []interface{}{[]interface{}{"type", "=", "lead"}}

// odoo19Catalog is the full research-backed semantic surface for the Odoo 19
// binding. This is DATA. Adding an operation never requires touching the
// executor.
var odoo19Catalog = []Odoo19Operation{
	{
		SemanticID: "identity.contact.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"base"},
		RequiredCapability: "identity.contact.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// type (contact/invoice/delivery/other) is a SOURCE_DISCOVERED addition, NOT covered by
		// the live evidence: the retail handoff reads it back to reuse an order's delivery address.
		ProviderModel: "res.partner",
		ReadFields:    []string{"id", "name", "is_company", "company_type", "email", "phone", "parent_id"},
		// Tax code, address and creation: what a review needs to find duplicate or incomplete partners.
		OptionalReadFields: []string{"customer_rank", "supplier_rank", "company_id", "ref", "type", "vat", "street", "city", "create_date", "create_uid"},
	},
	{
		SemanticID: "identity.contact.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"base"},
		RequiredCapability: "identity.contact.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel:      "res.partner",
		ReadFields:         []string{"id", "name", "is_company", "company_type", "email", "phone", "parent_id", "child_ids", "commercial_partner_id"},
		OptionalReadFields: []string{"customer_rank", "supplier_rank", "company_id", "ref"},
	},
	{
		SemanticID: "identity.contact.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"base"},
		RequiredCapability: "identity.contact.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "res.partner", WriteMode: WriteModeCreate,
		// type and street are SOURCE_DISCOVERED additions, NOT covered by the live evidence: the
		// retail handoff creates the order's delivery address as a child contact (type=delivery,
		// parent_id=customer) so the Odoo picking carries the ship-to address.
		AllowedFields: []string{"name", "is_company", "company_type", "email", "phone", "parent_id", "supplier_rank", "customer_rank", "company_id", "ref", "type", "street"},
		Relations: map[string]RelationSpec{
			"company_id": {TargetModel: "res.company"},
		},
		Verification: VerificationSpec{Field: "name", ExpectChange: false},
	},
	{
		SemanticID: "identity.contact.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"base"},
		RequiredCapability: "identity.contact.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "res.partner", WriteMode: WriteModeUpdate,
		AllowedFields: []string{"name", "email", "phone", "parent_id", "is_company", "street", "city", "vat"},
		Relations: map[string]RelationSpec{
			"parent_id": {TargetModel: "res.partner", SearchField: "name"},
		},
		PostconditionVerifyOperation: "identity.contact.describe",
		PostconditionComparison:      "EQUALS",
		PostconditionObservedPath:    "name",
		PostconditionExpectedSource:  "FROM_REQUEST_FIELD",
		PostconditionExpectedField:   "name",
	},
	{
		SemanticID: "people.employee.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.employee.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.employee", ReadFields: []string{"id", "name", "department_id", "parent_id", "job_id", "active", "resource_calendar_id"},
		// The employee's own contact, which holds their bank accounts: a review matches it with suppliers'.
		OptionalReadFields: []string{"work_contact_id"},
	},
	{
		SemanticID: "people.employee.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.employee.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.employee", ReadFields: []string{"id", "name", "department_id", "parent_id", "coach_id", "job_id", "active", "resource_calendar_id", "resource_id"},
	},
	{
		SemanticID: "people.employee.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.employee.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.employee", WriteMode: WriteModeCreate,
		AllowedFields: []string{"name", "department_id", "parent_id", "job_id"},
		Relations: map[string]RelationSpec{
			"department_id": {TargetModel: "hr.department", SearchField: "name"},
			"parent_id":     {TargetModel: "hr.employee", SearchField: "name"},
			"job_id":        {TargetModel: "hr.job", SearchField: "name"},
		},
	},
	{
		SemanticID: "people.employee.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.employee.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.employee", WriteMode: WriteModeUpdate,
		// NOTE (absorbed provider quirk): the manager field is parent_id, NOT
		// manager_id (that name only exists on hr.department). The stable
		// AERP field name exposed to callers is "manager_id"; the adapter's
		// FIELD_WRITE strategy performs the provider-field remap declared in
		// FieldAliases below so the caller never needs to know this.
		AllowedFields:    []string{"name", "department_id", "parent_id", "job_id"},
		FieldAliasesJSON: `{"manager_id":"parent_id"}`,
		Relations: map[string]RelationSpec{
			"department_id": {TargetModel: "hr.department", SearchField: "name"},
			"parent_id":     {TargetModel: "hr.employee", SearchField: "name"},
			"job_id":        {TargetModel: "hr.job", SearchField: "name"},
		},
	},
	{
		SemanticID: "people.employee.offboard", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.employee.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.employee", Method: "action_archive",
		Verification:     VerificationSpec{Field: "active", Expect: false},
		KnownSideEffects: []string{"employee's coach_id and reporting employees' parent_id/coach_id may be cleared"},
	},
	{
		SemanticID: "people.department.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.department.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.department", ReadFields: []string{"id", "name", "parent_id", "manager_id", "member_ids"},
	},
	{
		SemanticID: "people.department.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.department.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "hr.department",
		ReadFields:    []string{"id", "name", "complete_name", "manager_id", "parent_id", "company_id", "member_ids", "total_employee", "active"},
	},
	{
		SemanticID: "people.department.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.department.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.department", WriteMode: WriteModeCreate,
		AllowedFields: []string{"name", "parent_id", "manager_id"},
		Relations: map[string]RelationSpec{
			"parent_id":  {TargetModel: "hr.department", SearchField: "name"},
			"manager_id": {TargetModel: "hr.employee", SearchField: "name"},
		},
	},
	{
		SemanticID: "people.department.delete", Kind: KindCommand, Strategy: StrategyDestructiveGuarded,
		RequiredModules:    []string{"hr"},
		RequiredCapability: "people.department.delete", Risk: RiskHigh, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "hr.department", ImpactModel: "hr.employee", ImpactField: "department_id",
		KnownSideEffects: []string{"Odoo itself does NOT block this delete; without ASAO's guard, employees are silently orphaned (department_id set to false)"},
	},
	{
		SemanticID: "work.project.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.project.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.project", ReadFields: []string{"id", "name", "partner_id", "user_id", "active"},
	},
	{
		SemanticID: "work.project.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.project.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.project", WriteMode: WriteModeCreate,
		AllowedFields: []string{"name", "partner_id", "user_id"},
		Relations: map[string]RelationSpec{
			"partner_id": {TargetModel: "res.partner", SearchField: "name"},
			"user_id":    {TargetModel: "res.users", SearchField: "name"},
		},
	},
	{
		SemanticID: "work.project.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.project.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.project", WriteMode: WriteModeUpdate,
		// project.project has no lifecycle state: PRECONDITION_NOT_REQUIRED, deliberately.
		// stage_id is excluded on purpose — it is group-gated behind "Use Stages on Project"
		// and a read of it fails live with "You do not have enough rights to access the field
		// 'stage_id'". active is excluded because archiving is a different semantic act;
		// privacy_visibility is excluded as security-relevant.
		// date_start is excluded because the provider silently discards it. Proven live: a plain Odoo
		// write({'date_start': ...}) returns True while the value never lands. The cause is in
		// addons/project/models/project_project.py:666-678 — when date_start is written without the
		// paired 'date' (Expiration) field and the record has none, Odoo executes del vals['date_start'].
		// There is also an SQL constraint check(date >= date_start) at line 187. The field is therefore
		// only writable as part of a date_start+date pair, and the catalog cannot express a
		// must-be-supplied-together constraint, so declaring it alone would advertise a capability that
		// silently no-ops.
		AllowedFields: []string{"name", "user_id", "partner_id"},
		Relations: map[string]RelationSpec{
			"user_id":    {TargetModel: "res.users"},
			"partner_id": {TargetModel: "res.partner", SearchField: "name"},
		},
	},
	{
		SemanticID: "work.project.delete", Kind: KindCommand, Strategy: StrategyDestructiveGuarded,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.project.delete", Risk: RiskHigh, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.project", ImpactModel: "project.task", ImpactField: "project_id",
		AdditionalImpacts: []ImpactSpec{{Model: "project.milestone", Field: "project_id"}},
		KnownSideEffects:  []string{"Odoo itself CASCADE-DELETES live tasks and milestones with zero warning; ASAO's guard closes this gap"},
	},
	{
		SemanticID: "work.task.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.task.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.task", ReadFields: []string{"id", "name", "project_id", "stage_id", "state", "user_ids"},
		// Dates, hours and customer: what a delivery or workload statistic groups and sums by. Priority,
		// tags and the recurrence: how a routine sorts the day's work into daily, weekly and monthly.
		OptionalReadFields: []string{"create_date", "date_deadline", "date_last_stage_update", "allocated_hours", "effective_hours", "partner_id", "milestone_id",
			"priority", "tag_ids", "recurring_task", "repeat_interval", "repeat_unit", "repeat_type", "repeat_until"},
	},
	{
		SemanticID: "work.task.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.task.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.task", ReadFields: []string{"id", "name", "project_id", "stage_id", "state", "user_ids", "parent_id", "milestone_id"},
		OptionalReadFields: []string{"description", "date_deadline", "priority", "tag_ids", "recurring_task", "recurring_count",
			"repeat_interval", "repeat_unit", "repeat_type", "repeat_until"},
	},
	{
		SemanticID: "work.task.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.task.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.task", WriteMode: WriteModeCreate,
		// A recurring task: recurring_task with the repeat fields makes Odoo create the task's
		// project.task.recurrence itself (project_task.py create). Odoo then makes the next occurrence
		// when this one is closed (done or cancelled), not on a calendar.
		AllowedFields: []string{"name", "project_id", "user_ids", "parent_id", "description", "date_deadline", "priority", "tag_ids",
			"recurring_task", "repeat_interval", "repeat_unit", "repeat_type", "repeat_until"},
		Relations: map[string]RelationSpec{
			"project_id": {TargetModel: "project.project", SearchField: "name"},
			"parent_id":  {TargetModel: "project.task", SearchField: "name"},
			"user_ids":   {TargetModel: "res.users", SearchField: "name", Multi: true},
			"tag_ids":    {TargetModel: "project.tags", SearchField: "name", Multi: true},
		},
	},
	{
		SemanticID: "work.task.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.task.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// NOTE (absorbed provider quirk): state and stage_id are orthogonal
		// in Odoo 19 (kanban_state is gone). No action_close method exists;
		// core Odoo drives this lifecycle via direct field writes, so
		// FIELD_WRITE is the empirically-correct strategy, not METHOD_CALL.
		ProviderModel: "project.task", WriteMode: WriteModeUpdate,
		AllowedFields: []string{"name", "state", "stage_id", "user_ids", "date_deadline", "description", "priority", "tag_ids",
			"recurring_task", "repeat_interval", "repeat_unit", "repeat_type", "repeat_until"},
		Relations: map[string]RelationSpec{
			"stage_id": {TargetModel: "project.task.type", SearchField: "name"},
			"user_ids": {TargetModel: "res.users", SearchField: "name", Multi: true},
			"tag_ids":  {TargetModel: "project.tags", SearchField: "name", Multi: true},
		},
		PostconditionVerifyOperation: "work.task.describe",
		PostconditionComparison:      "EQUALS",
		PostconditionObservedPath:    "name",
		PostconditionExpectedSource:  "FROM_REQUEST_FIELD",
		PostconditionExpectedField:   "name",
	},
	{
		// A pure transition: it only ever flips an EXISTING milestone's
		// reached state. It previously also doubled as a create (WriteMode
		// Upsert, selecting create-vs-update purely by "was a target id
		// supplied"), which is what let two identical `reach` invocations
		// silently create two separate milestones rather than transition
		// one — exactly the create/update conflation `people.employee.update`
		// was already fixed against. See work.milestone.create/list/describe
		// for the create/read half of this entity's lifecycle.
		SemanticID: "work.milestone.reach", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.milestone.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.milestone", WriteMode: WriteModeUpdate,
		AllowedFields: []string{"is_reached", "deadline"},
		Verification:  VerificationSpec{Field: "is_reached", Expect: true},
	},
	{
		SemanticID: "work.milestone.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.milestone.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.milestone", ReadFields: []string{"id", "name", "project_id", "deadline", "is_reached"},
	},
	{
		SemanticID: "work.milestone.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.milestone.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.milestone", ReadFields: []string{"id", "name", "project_id", "deadline", "is_reached"},
	},
	{
		SemanticID: "work.milestone.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.milestone.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "project.milestone", WriteMode: WriteModeCreate,
		AllowedFields: []string{"name", "deadline", "project_id"},
		Relations: map[string]RelationSpec{
			"project_id": {TargetModel: "project.project", SearchField: "name"},
		},
	},
	{
		SemanticID: "crm.lead.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"crm"},
		RequiredCapability: "crm.lead.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "crm.lead", ReadFields: []string{"id", "name", "type", "stage_id", "partner_id", "expected_revenue", "probability"},
		// Dates, salesperson, team and lost reason: what a pipeline statistic groups by.
		OptionalReadFields: []string{"create_date", "date_deadline", "date_closed", "user_id", "team_id", "lost_reason_id"},
	},
	{
		SemanticID: "crm.lead.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"crm"},
		RequiredCapability: "crm.lead.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "crm.lead", ReadFields: []string{"id", "name", "type", "stage_id", "partner_id", "expected_revenue", "probability", "active", "team_id"},
	},
	{
		SemanticID: "crm.lead.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"crm"},
		RequiredCapability: "crm.lead.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "crm.lead", WriteMode: WriteModeCreate,
		AllowedFields: []string{"name", "type", "partner_id", "expected_revenue", "email_from"},
		Relations: map[string]RelationSpec{
			"partner_id": {TargetModel: "res.partner", SearchField: "name"},
		},
	},
	{
		SemanticID: "crm.lead.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"crm"},
		RequiredCapability: "crm.lead.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "crm.lead", WriteMode: WriteModeUpdate,
		// stage_id is deliberately NOT writable here. Moving a lead through the pipeline is a
		// semantic transition, and this catalog already models CRM transitions as their own verbs
		// (crm.opportunity.convert / win / lose). Allowing a generic update to set stage_id would
		// let a caller move pipeline state with no semantic verb and no verification.
		AllowedFields: []string{"name", "partner_id", "user_id", "expected_revenue", "probability", "phone", "email_from"},
		Relations: map[string]RelationSpec{
			"partner_id": {TargetModel: "res.partner", SearchField: "name"},
			"user_id":    {TargetModel: "res.users", SearchField: "name"},
		},
	},
	{
		SemanticID: "crm.opportunity.convert", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"crm"},
		RequiredCapability: "crm.lead.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// NOTE (absorbed provider quirk): crm.lead.convert_opportunity(partner,
		// user_ids, team_id) expects `partner` as an ORM recordset and raises
		// AttributeError over generic RPC. The safe, empirically-verified
		// equivalent is a direct field write of type=opportunity.
		// The conversion itself is the operation's whole meaning, so the
		// binding performs it: `type` is forced here rather than being an
		// input the caller has to know the provider's enum spelling for.
		// Before this, `convert --target-id N` wrote nothing at all and still
		// reported VERIFIED, because nothing was declared to verify.
		ProviderModel: "crm.lead", WriteMode: WriteModeUpdate,
		ProviderDomain: leadInputDomain,
		ForcedValues:   map[string]interface{}{"type": "opportunity"},
		AllowedFields:  []string{"stage_id"},
		Relations: map[string]RelationSpec{
			"stage_id": {TargetModel: "crm.stage", SearchField: "name"},
		},
		Verification: VerificationSpec{Field: "type", Expect: "opportunity"},
	},
	{
		SemanticID: "crm.opportunity.win", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"crm"},
		RequiredCapability: "crm.lead.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "crm.lead", Method: "action_set_won",
		ProviderDomain: opportunityDomain,
		Verification:   VerificationSpec{Field: "probability", Expect: float64(100)},
	},
	{
		SemanticID: "crm.opportunity.lose", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"crm"},
		RequiredCapability: "crm.lead.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// Empirically returns None/void over RPC (confirmed: JSON-RPC body
		// omits "result" entirely). Executor must treat that as success, not
		// a decode failure — this is the production fix for the known LAB
		// "unexpected end of JSON input" defect.
		ProviderModel: "crm.lead", Method: "action_set_lost",
		ProviderDomain: opportunityDomain,
		Verification:   VerificationSpec{Field: "active", Expect: false},
	},
	{
		SemanticID: "sales.quotation.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "sale.order",
		ReadFields:    []string{"id", "name", "state", "partner_id", "amount_total", "invoice_status"},
		// Dates, salesperson, team and untaxed amount: what a sales statistic groups and sums by.
		OptionalReadFields: []string{"company_id", "client_order_ref", "warehouse_id", "date_order", "validity_date", "user_id", "team_id", "amount_untaxed", "currency_id"},
	},
	{
		SemanticID: "sales.order.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// picking_ids and opportunity_id are optional because stock and crm are optional in Odoo 19.
		// Note: added fields company_id, client_order_ref, order_line and optional field warehouse_id
		// (sale_stock) are SOURCE_DISCOVERED additions NOT covered by previous live evidence.
		// amount_tax and partner_shipping_id are SOURCE_DISCOVERED additions as well (both are
		// stored sale.order fields of the sale module): the retail handoff reconciles the order's
		// tax and ship-to address against the checkout before it confirms.
		ProviderModel:      "sale.order",
		ReadFields:         []string{"id", "name", "state", "partner_id", "amount_total", "currency_id", "invoice_status", "invoice_ids", "company_id", "client_order_ref", "order_line", "amount_tax", "partner_shipping_id"},
		OptionalReadFields: []string{"picking_ids", "opportunity_id", "warehouse_id"},
		DefaultSort:        "id asc",
	},
	{
		SemanticID: "sales.order.line.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		// Note: added fields product_uom_id and company_id are SOURCE_DISCOVERED additions NOT covered by previous live evidence.
		ProviderModel: "sale.order.line", ReadFields: []string{"id", "name", "order_id", "product_id", "product_uom_qty", "qty_delivered", "qty_invoiced", "price_unit", "price_subtotal", "state", "product_uom_id", "company_id"},
	},
	{
		SemanticID: "sales.quotation.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "sale.order", WriteMode: WriteModeCreate,
		// partner_shipping_id is a SOURCE_DISCOVERED addition (writable, stored on sale.order in
		// Odoo 19): the retail handoff sets the order's delivery contact as the ship-to address.
		AllowedFields: []string{"partner_id", "opportunity_id", "client_order_ref", "company_id", "warehouse_id", "partner_shipping_id"},
		Relations: map[string]RelationSpec{
			"partner_id":          {TargetModel: "res.partner", SearchField: "name"},
			"company_id":          {TargetModel: "res.company"},
			"warehouse_id":        {TargetModel: "stock.warehouse"},
			"partner_shipping_id": {TargetModel: "res.partner"},
		},
	},
	{
		SemanticID: "sales.quotation.line.add", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// price_unit is writable and stored on sale.order.line in Odoo 19
		// (fields_get: readonly=false, store=true), so a caller can set the
		// agreed price instead of silently inheriting the product's list
		// price. Its absence here was an omission, not a provider limit —
		// procurement.rfq.line.add already exposed the same field.
		ProviderModel: "sale.order.line", WriteMode: WriteModeCreate,
		AllowedFields: []string{"order_id", "product_id", "product_uom_qty", "price_unit"},
		Relations: map[string]RelationSpec{
			"order_id":   {TargetModel: "sale.order"},
			"product_id": {TargetModel: "product.product", SearchField: "name"},
		},
	},
	{
		SemanticID: "sales.order.line.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "sale.order.line", WriteMode: WriteModeUpdate,
		// Read-before-write: the line is inspectable before and after via
		// sales.order.line.describe. execFieldWrite enforces this Precondition before contacting
		// the provider, so a confirmed or cancelled parent order cannot be edited here.
		AllowedFields: []string{"product_uom_qty", "price_unit", "discount"},
		Precondition:  VerificationSpec{Field: "state", Expect: "draft"},
	},
	{
		SemanticID: "sales.quotation.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "sale.order", WriteMode: WriteModeUpdate,
		// Quotation-only by design. sale.order.state live selection is
		// draft / sent / sale / cancel; execFieldWrite enforces this Precondition before
		// contacting the provider, so a confirmed or cancelled order cannot be edited here.
		// The fields are the quotation-specific ones: expiration, customer reference and
		// order date. Nothing that would restate the commercial content of a live order.
		AllowedFields: []string{"validity_date", "client_order_ref", "date_order"},
		Precondition:  VerificationSpec{Field: "state", Expect: "draft"},
	},
	{
		SemanticID: "sales.order.confirm", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredCapability: "sales.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		RequiredModules: []string{"sale"},
		ProviderModel:   "sale.order", Method: "action_confirm",
		Verification:     VerificationSpec{Field: "state", Expect: "sale"},
		KnownSideEffects: []string{"auto-creates a stock.picking delivery for storable (is_storable=true) products"},
	},
	{
		SemanticID: "sales.invoice.create", Kind: KindAction, Strategy: StrategyWizard,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "finance.invoice.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel:   "sale.order",
		WizardModel:     "sale.advance.payment.inv",
		WizardLinkField: "sale_order_ids", WizardLinkMulti: true,
		WizardMethod:     "create_invoices",
		WizardValues:     map[string]interface{}{"advance_payment_method": "delivered"},
		KnownSideEffects: []string{"creates an account.move (out_invoice); the sale-side requires this wizard while the mirror procurement.bill.create is a single direct method call — an Odoo API-shape asymmetry the semantic layer intentionally hides"},
	},
	{
		SemanticID: "procurement.rfq.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order", ReadFields: []string{"id", "name", "state", "partner_id", "amount_total", "invoice_status"},
		// Dates, buyer and untaxed amount: what a purchasing statistic groups and sums by.
		OptionalReadFields: []string{"date_order", "date_approve", "date_planned", "user_id", "amount_untaxed", "currency_id"},
	},
	{
		SemanticID: "procurement.order.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "purchase.order", ReadFields: []string{"id", "name", "partner_id", "state", "date_order", "date_approve", "currency_id", "company_id", "user_id", "origin", "picking_ids", "invoice_ids", "order_line", "amount_untaxed", "amount_tax", "amount_total", "invoice_status"},
	},
	{
		SemanticID: "procurement.order.line.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "purchase.order.line", ReadFields: []string{"id", "name", "order_id", "product_id", "product_qty", "qty_received", "qty_invoiced", "price_unit", "price_subtotal", "date_planned", "state", "product_uom_id"},
	},
	{
		SemanticID: "procurement.rfq.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order", WriteMode: WriteModeCreate,
		AllowedFields: []string{"partner_id"},
		Relations: map[string]RelationSpec{
			"partner_id": {TargetModel: "res.partner", SearchField: "name"},
		},
	},
	{
		SemanticID: "procurement.rfq.line.add", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order.line", WriteMode: WriteModeCreate,
		AllowedFields: []string{"order_id", "product_id", "product_qty", "price_unit"},
		Relations: map[string]RelationSpec{
			"order_id":   {TargetModel: "purchase.order"},
			"product_id": {TargetModel: "product.product", SearchField: "name"},
		},
	},
	{
		SemanticID: "procurement.order.line.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order.line", WriteMode: WriteModeUpdate,
		// Read-before-write: the line is inspectable before and after via
		// procurement.order.line.describe. execFieldWrite enforces this Precondition before
		// contacting the provider, so a confirmed or cancelled parent order cannot be edited here.
		AllowedFields: []string{"product_qty", "price_unit", "date_planned"},
		Precondition:  VerificationSpec{Field: "state", Expect: "draft"},
	},
	{
		SemanticID: "procurement.rfq.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order", WriteMode: WriteModeUpdate,
		// RFQ-only by design. purchase.order.state live selection is
		// draft / sent / to approve / purchase / cancel — note it is NOT the sale.order set,
		// the two families are non-symmetric. execFieldWrite enforces this Precondition before
		// contacting the provider. 'notes' is deliberately absent: it does not exist on
		// purchase.order in this build and a read of it raises
		// "Invalid field 'notes' on 'purchase.order'".
		AllowedFields: []string{"partner_ref", "date_planned", "date_order"},
		Precondition:  VerificationSpec{Field: "state", Expect: "draft"},
	},
	{
		SemanticID: "procurement.order.confirm", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order", Method: "button_confirm",
		Verification:     VerificationSpec{Field: "state", Expect: "purchase"},
		KnownSideEffects: []string{"auto-creates a stock.picking receipt, starts 'assigned' immediately (no reservation contention on the receiving side)"},
	},
	{
		SemanticID: "procurement.bill.create", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "finance.invoice.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order", Method: "action_create_invoice", MethodArgs: []interface{}{[]interface{}{}},
		KnownSideEffects: []string{"creates an account.move (in_invoice) directly — no wizard, unlike sales.invoice.create"},
	},
	{
		SemanticID: "inventory.transfer.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "stock.picking", ReadFields: []string{"id", "name", "state", "picking_type_id", "origin"},
	},
	{
		SemanticID: "inventory.transfer.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		// sale_id and purchase_id are not native to stock.picking: the sale_stock and purchase_stock
		// bridge modules add them, and both are optional in Odoo 19 (V2_16 F5, same reason as
		// qty_available / is_storable / picking_ids). Requesting them unconditionally made this
		// operation fail outright on an Odoo with `stock` but without `purchase` -- observed live in
		// the V2.5 Gate #3 managed conformance run as AERP_VALIDATION_FAILED
		// "Invalid field 'purchase_id' on 'stock.picking'".
		// DefaultSort "id asc" added for deterministic reads.
		ProviderModel:      "stock.picking",
		ReadFields:         []string{"id", "name", "state", "picking_type_id", "origin", "partner_id", "location_id", "location_dest_id", "scheduled_date", "date_done", "company_id", "backorder_id", "move_ids"},
		OptionalReadFields: []string{"sale_id", "purchase_id", "picking_type_code"},
		DefaultSort:        "id asc",
	},
	{
		SemanticID: "inventory.move.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		// Note: added fields company_id, product_uom and optional field sale_line_id (sale_stock)
		// are SOURCE_DISCOVERED additions NOT covered by previous live evidence.
		ProviderModel:      "stock.move",
		ReadFields:         []string{"id", "reference", "picking_id", "product_id", "product_uom_qty", "quantity", "state", "location_id", "location_dest_id", "date", "company_id", "product_uom"},
		OptionalReadFields: []string{"sale_line_id"},
		DefaultSort:        "id asc",
	},
	{
		SemanticID: "inventory.transfer.reserve", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "stock.picking", Method: "action_assign",
		KnownSideEffects: []string{"never raises on insufficient stock; state may remain 'confirmed' rather than 'assigned' — caller must check resulting state, not just call success"},
	},
	{
		SemanticID: "inventory.transfer.validate", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "stock.picking", Method: "button_validate",
		Verification: VerificationSpec{Field: "state", Expect: "done"},
	},
	{
		SemanticID: "inventory.transfer.cancel", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "stock.picking", Method: "action_cancel",
		Verification: VerificationSpec{Field: "state", Expect: "cancel"},
	},
	{
		SemanticID: "inventory.stock_level.read", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules: []string{"stock"},
		// RequiredCapability is unhyphenated because a hyphen makes the capability
		// ungrantable under the canonical regex, and DEV's governance catalogue defines
		// the unhyphenated key ("inventory.stocklevel.read").
		RequiredCapability: "inventory.stocklevel.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "stock.quant", ReadFields: []string{"id", "product_id", "location_id", "quantity", "reserved_quantity"},
	},
	{
		SemanticID: "finance.invoice.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "account.move", ReadFields: []string{"id", "name", "move_type", "state", "payment_state", "amount_total", "partner_id"},
		// Dates, amounts due and untaxed, salesperson and journal: what a revenue or receivables
		// statistic groups and sums by.
		// The supplier's own reference, the invoice a credit note reverses, and who created it when:
		// what a review needs to find a bill entered twice or a refund after payment.
		OptionalReadFields: []string{"invoice_date", "invoice_date_due", "date", "amount_untaxed", "amount_residual", "currency_id", "invoice_user_id", "journal_id",
			"ref", "reversed_entry_id", "create_uid", "create_date"},
		ProviderDomain: invoiceDocumentDomain,
	},
	{
		SemanticID: "finance.invoice.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "account.move", ReadFields: []string{"id", "name", "move_type", "state", "payment_state", "amount_total", "amount_residual", "partner_id", "invoice_date"},
		ProviderDomain: invoiceDocumentDomain,
	},
	{
		SemanticID: "finance.invoice.line.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.move.line",
		// account.move.line stores both invoice lines and pure journal-entry lines
		// (move_type 'entry'); Terra proved live that this constraint returns line id
		// 21 (invoice) and excludes line id 29 (a PBNK1 payment journal entry). The
		// allowlist is explicit rather than a "!= entry" denylist so a new document
		// type cannot silently join.
		ProviderDomain: invoiceLineDocumentDomain,
		ReadFields:     []string{"id", "name", "move_id", "account_id", "partner_id", "quantity", "price_unit", "price_subtotal", "balance", "debit", "credit", "product_id", "date"},
	},
	{
		SemanticID: "finance.invoice.line.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.move.line",
		// Same invoice-line constraint as finance.invoice.line.describe: account.move.line
		// holds both invoice lines and pure journal-entry lines. execRead ANDs this into
		// every read, so a caller-supplied domain can narrow the result to one parent
		// invoice but can never widen it back to journal-entry lines.
		ProviderDomain: invoiceLineDocumentDomain,
		ReadFields:     []string{"id", "name", "move_id", "account_id", "partner_id", "quantity", "price_unit", "price_subtotal", "balance", "debit", "credit", "product_id", "date", "display_type"},
		// The taxes a line carries and its document's type: a review compares them with the product's.
		OptionalReadFields: []string{"tax_ids", "move_type"},
	},
	{
		SemanticID: "finance.invoice.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// Empirically required before posting a vendor bill (in_invoice) —
		// unlike customer invoices, invoice_date is not auto-defaulted.
		ProviderModel: "account.move", WriteMode: WriteModeUpdate,
		ProviderDomain: invoiceDocumentDomain,
		AllowedFields:  []string{"invoice_date"},
	},
	{
		SemanticID: "finance.invoice.post", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.write", Risk: RiskHigh, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "account.move", Method: "action_post",
		ProviderDomain:   invoiceDocumentDomain,
		Verification:     VerificationSpec{Field: "state", Expect: "posted"},
		KnownSideEffects: []string{"legal document number is assigned only here, not on create; vendor bills (in_invoice) additionally require invoice_date to already be set or this fails with a BUSINESS_PRECONDITION_FAILED"},
	},
	{
		SemanticID: "finance.invoice.cancel", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "account.move", Method: "button_cancel",
		// account.move holds both invoice documents and pure journal entries. Terra counted live: 6 of
		// 12 account.move records are move_type 'entry' (the payment journal entries). Without this
		// constraint finance.invoice.cancel could cancel a payment's journal entry. execMethodCall
		// gates on ProviderDomain and refuses an ineligible target before contacting the provider.
		ProviderDomain: invoiceDocumentDomain,
		Verification:   VerificationSpec{Field: "state", Expect: "cancel"},
	},
	{
		SemanticID: "finance.invoice.draft", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "account.move", Method: "button_draft",
		// Odoo's own guard, read from the provider source at
		// addons/account/models/account_move.py:6208, accepts BOTH 'posted' and 'cancel':
		//   if any(move.state not in ('cancel', 'posted') ...): raise UserError(...)
		// VerificationSpec.Expect holds a single value, so declaring a Precondition here
		// would wrongly forbid one of the two legal source states. Eligibility is instead
		// enforced by ProviderDomain (document type) plus Odoo's native state guard, both
		// proven live. This is a deliberate PRECONDITION_NOT_REQUIRED decision.
		ProviderDomain: invoiceDocumentDomain,
		Verification:   VerificationSpec{Field: "state", Expect: "draft"},
	},
	{
		// Odoo's own entry point, account.move.action_register_payment,
		// refuses a draft document ("You can only register payment for
		// posted journal entries.") before it ever builds the
		// account.payment.register wizard. Building that wizard directly
		// from account.move.line — which is what this operation must do,
		// since action_register_payment itself is unsafe to call generically
		// over RPC — does NOT inherit that check for free, so Precondition
		// enforces it explicitly here. Without it, this operation could mark
		// a draft invoice/bill "paid" and post real payment/journal entries
		// against a document that was never posted itself.
		//
		// The wizard's line_ids must be restricted to the document's own
		// open receivable/payable balance line(s), not every unreconciled
		// account.move.line on the move — a customer invoice or vendor bill
		// also carries income/expense/tax lines that are unreconciled by
		// construction (they reconcile within the same move, not against
		// cash), and handing the wizard all of them produced one real
		// payment plus two spurious 0.00 payments with their own posted
		// journal entries per call. account_type in
		// (asset_receivable, liability_payable) selects exactly the
		// balance-carrying line(s) for either document type, so the same
		// catalog entry stays correct for both invoices and bills.
		SemanticID: "finance.payment.register", Kind: KindAction, Strategy: StrategyWizard,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.payment.write", Risk: RiskHigh, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel:   "account.move",
		ProviderDomain:  invoiceDocumentDomain,
		WizardModel:     "account.payment.register",
		WizardLinkField: "line_ids", WizardLinkMulti: true,
		WizardLinkSourceModel: "account.move.line", WizardLinkSourceDomainField: "move_id",
		WizardLinkSourceExtraDomain: [][]interface{}{
			{"reconciled", "=", false},
			{"account_id.account_type", "in", []interface{}{"asset_receivable", "liability_payable"}},
		},
		WizardMethod: "action_create_payments",
		Precondition: VerificationSpec{Field: "state", Expect: "posted"},
		Verification: VerificationSpec{Field: "payment_state", Expect: "paid"},
		// state must STAY posted (registering a payment never reopens the
		// document) and amount_residual must actually reach zero — the
		// payment_state field alone is Odoo's own summary of both, but
		// checking it in isolation is exactly what let a wizard call
		// against a draft move report VERIFIED: draft moves compute
		// payment_state trivially, so a single-field check cannot tell
		// "the invariant held" from "the invariant was never in play".
		AdditionalVerifications: []VerificationSpec{
			{Field: "state", Expect: "posted"},
			{Field: "amount_residual", Expect: float64(0)},
		},
		KnownSideEffects: []string{"wizard auto-derives amount/journal/payment method from the target line(s); do not rely on active_model/active_ids context — set line_ids directly"},
	},
	{
		SemanticID: "finance.journal.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.journal.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.journal", ReadFields: []string{"id", "name", "type", "code", "company_id"},
	},
	{
		SemanticID: "finance.account.chart.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.account.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.account", ReadFields: []string{"id", "name", "code", "account_type"},
	},
	{
		SemanticID: "finance.tax.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.tax.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.tax", ReadFields: []string{"id", "name", "amount", "amount_type", "type_tax_use"},
	},
	{
		SemanticID: "finance.taxevidence.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.move.line",
		// Preserved from the pre-convergence DEV catalog, where it is consumed by
		// vn-compliance-service for compliance returns. Its authorization is
		// finance.invoice.read, and live evidence confirms tax evidence is invoice-bound in
		// this provider: of the lines carrying tax data, 30 of 30 with tax_line_id and 37 of
		// 37 with tax_ids sit on invoice documents, none on pure journal entries. Granting a
		// finance.invoice.read capability visibility into journal entries would be the
		// Residual X escape class, so the invoice-only constraint is enforced here rather
		// than assumed. The domain costs nothing today and closes the hole permanently.
		ProviderDomain: invoiceLineDocumentDomain,
		ReadFields:     []string{"id", "move_id", "tax_line_id", "tax_ids", "tax_base_amount", "balance", "date", "parent_state", "display_type", "company_id", "company_currency_id", "account_id"},
	},
	{
		SemanticID: "finance.payment_term.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules: []string{"account"},
		// RequiredCapability is unhyphenated because a hyphen makes the capability
		// ungrantable under the canonical regex, and DEV's governance catalogue defines
		// the unhyphenated key ("finance.paymentterm.read").
		RequiredCapability: "finance.paymentterm.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.payment.term", ReadFields: []string{"id", "name"},
	},
	{
		SemanticID: "catalog.product.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"product"},
		RequiredCapability: "catalog.product.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// is_storable moved to OptionalReadFields because module `stock` is optional in Odoo 19 (V2_16 F5).
		ProviderModel: "product.template",
		ReadFields:    []string{"id", "name", "type", "list_price", "sale_ok", "purchase_ok"},
		// The cost: what a review compares a sale price with.
		OptionalReadFields: []string{"is_storable", "standard_price"},
	},
	{
		SemanticID: "catalog.product.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"product"},
		RequiredCapability: "catalog.product.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// is_storable moved to OptionalReadFields because module `stock` is optional in Odoo 19 (V2_16 F5).
		ProviderModel:      "product.template",
		ReadFields:         []string{"id", "name", "type", "list_price", "sale_ok", "purchase_ok", "product_variant_ids", "uom_id"},
		OptionalReadFields: []string{"is_storable"},
	},
	{
		SemanticID: "catalog.product_variant.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"product"},
		RequiredCapability: "catalog.product.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		// Note: added field company_id is a SOURCE_DISCOVERED addition NOT covered by previous live evidence.
		ProviderModel: "product.product", ReadFields: []string{"id", "name", "default_code", "barcode", "product_tmpl_id", "uom_id", "active", "company_id"},
		// DefaultSort is "id asc" SPECIFICALLY so a caller can page through the full catalog with a
		// keyset Domain clause ([["id", ">", lastSeenID]]) rather than trusting Odoo's unspecified
		// natural order across repeated calls, which is not guaranteed stable and could skip or
		// repeat rows across pages. execRead honors a caller Domain but NOT a caller Order (it always
		// uses this DefaultSort), which is exactly what makes id-ascending order here a reliable,
		// caller-independent pagination contract rather than an incidental default. See
		// services/commerce-service/internal/integration/product_catalog_sync.go's own Run for the
		// caller that depends on this.
		DefaultSort: "id asc",
	},
	{
		SemanticID: "catalog.product_variant.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"product"},
		RequiredCapability: "catalog.product.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		// qty_available moved to OptionalReadFields because module `stock` is optional in Odoo 19
		// (V2_16 F5, same reason as is_storable and picking_ids). It is a stock-computed field on
		// product.product, so requesting it unconditionally made this operation fail outright on
		// any Odoo without `stock` -- observed live in the V2.5 Gate #3 managed conformance run as
		// AERP_VALIDATION_FAILED "Invalid field 'qty_available' on 'product.product'", while the
		// sibling catalog.product_variant.list, which never requested it, succeeded.
		ProviderModel:      "product.product",
		ReadFields:         []string{"id", "name", "default_code", "barcode", "product_tmpl_id", "uom_id", "active", "lst_price", "standard_price"},
		OptionalReadFields: []string{"qty_available"},
	},
	{
		SemanticID: "catalog.product.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"product"},
		RequiredCapability: "catalog.product.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		// NOTE (absorbed provider quirk): 'type' no longer has a storable
		// value in Odoo 19 ('consu'|'service'|'combo'); storage tracking is
		// the independent is_storable boolean.
		ProviderModel: "product.template", WriteMode: WriteModeCreate,
		AllowedFields: []string{"name", "type", "is_storable", "list_price", "sale_ok", "purchase_ok", "taxes_id"},
		Relations: map[string]RelationSpec{
			// An empty many2many replace command deliberately removes the provider's
			// default sales taxes from disposable integration products. The field is
			// only used when the caller supplies it; ordinary product creation keeps
			// the provider default behavior.
			"taxes_id": {TargetModel: "account.tax", Multi: true},
		},
	},
	{
		SemanticID: "catalog.product.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"product"},
		RequiredCapability: "catalog.product.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "product.template", WriteMode: WriteModeUpdate,
		AllowedFields: []string{"list_price", "sale_ok", "purchase_ok"},
	},
	{
		SemanticID: "schedule.event.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"calendar"},
		RequiredCapability: "schedule.event.read", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "calendar.event", ReadFields: []string{"id", "name", "start", "stop", "allday", "partner_ids"},
	},
	{
		SemanticID: "schedule.event.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"calendar"},
		RequiredCapability: "schedule.event.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "calendar.event",
		ReadFields:    []string{"id", "name", "start", "stop", "allday", "duration", "partner_ids", "user_id", "description", "location"},
	},
	{
		SemanticID: "schedule.event.create", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"calendar"},
		RequiredCapability: "schedule.event.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "calendar.event", WriteMode: WriteModeCreate,
		AllowedFields: []string{"name", "start", "stop", "allday", "partner_ids"},
	},
	{
		SemanticID: "schedule.event.update", Kind: KindCommand, Strategy: StrategyFieldWrite,
		RequiredModules:    []string{"calendar"},
		RequiredCapability: "schedule.event.write", Risk: RiskLow, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "calendar.event", WriteMode: WriteModeUpdate,
		// calendar.event has NO lifecycle state field, so there is no state to gate on:
		// this is a deliberate PRECONDITION_NOT_REQUIRED decision, not an omission.
		// Temporal consistency is enforced by the provider itself — _check_closing_date at
		// addons/calendar/models/calendar_event.py:451 raises ValidationError when stop < start
		// for a non-allday event. allday is deliberately NOT writable here because it switches
		// the record onto start_date/stop_date semantics (_inverse_dates forces 08:00/18:00),
		// which would silently rewrite the very times the caller just set.
		AllowedFields: []string{"name", "start", "stop", "location"},
	},
	{
		SemanticID: "finance.payment.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.payment", ReadFields: []string{"id", "name", "partner_id", "amount", "currency_id", "date", "state", "payment_type", "journal_id", "move_id"},
		// The bank account paid into, the bills and invoices the payment settles, and who made it when:
		// what a review needs to see a payment split under a limit, sent to another account, or made
		// by the person who created the vendor.
		OptionalReadFields: []string{"is_reconciled", "memo", "partner_type", "partner_bank_id", "reconciled_bill_ids", "reconciled_invoice_ids",
			"create_uid", "create_date"},
	},
	{
		SemanticID: "finance.payment.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"account"},
		RequiredCapability: "finance.invoice.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "account.payment",
		ReadFields:    []string{"id", "name", "state", "partner_id", "payment_type", "partner_type", "amount", "currency_id", "date", "journal_id", "payment_method_line_id", "is_reconciled", "company_id", "memo"},
	},
	{
		SemanticID: "work.project.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"project"},
		RequiredCapability: "work.project.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "project.project", ReadFields: []string{"id", "name", "partner_id", "user_id", "company_id", "active", "date_start", "date", "privacy_visibility", "task_count"},
	},
	{
		SemanticID: "inventory.location.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		ProviderModel: "stock.location", ReadFields: []string{"id", "complete_name", "name", "usage", "location_id", "company_id", "active", "barcode", "warehouse_id"},
	},
	{
		SemanticID: "structure.company.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"base"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		// Provider-structure discovery, preserved from the pre-convergence DEV catalog where
		// it is consumed by the api-gateway product allowlist and the CLI discovery command.
		// res.company holds exactly one kind of record, so no subtype constraint applies and
		// no ProviderDomain is declared — this is a deliberate decision, not an omission.
		// An ASAO Organization is NOT an Odoo Company; this operation resolves the provider's
		// company identities only and implies no mapping between the two.
		ProviderModel: "res.company", ReadFields: []string{"id", "name", "parent_id", "currency_id"},
		DefaultSort: "id asc",
	},
	{
		SemanticID: "structure.warehouse.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "LIVE_READ_VERIFIED",
		// Provider-structure discovery, preserved from the pre-convergence DEV catalog.
		// stock.warehouse holds exactly one kind of record, so no subtype constraint applies
		// and no ProviderDomain is declared — a deliberate decision.
		// An ASAO Branch is NOT an Odoo Warehouse; no implicit mapping is created here.
		// Legacy semantic evidence was for its earlier field shape (id, name, code, company_id, view_location_id).
		// Newly added fields (lot_stock_id, out_type_id, active, delivery_steps) are a SOURCE_DISCOVERED extension
		// verified against official Odoo 19 source (https://github.com/odoo/odoo/blob/19.0/addons/stock/models/stock_warehouse.py);
		// they do not claim live verification or manifest promotion until canonical live evidence is gathered.
		ProviderModel: "stock.warehouse", ReadFields: []string{"id", "name", "code", "company_id", "lot_stock_id", "out_type_id", "view_location_id", "active", "delivery_steps"},
		DefaultSort: "id asc",
	},
	{
		SemanticID: "structure.operation_type.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		// Provider-structure discovery for stock picking types.
		// Field schema verified against official Odoo 19 source (https://github.com/odoo/odoo/blob/19.0/addons/stock/models/stock_picking.py).
		// Marked SOURCE_DISCOVERED with no live manifest promotion pending real provider evidence.
		ProviderModel: "stock.picking.type", ReadFields: []string{"id", "company_id", "warehouse_id", "code", "reservation_method", "default_location_src_id", "active"},
		DefaultSort: "id asc",
	},
	{
		SemanticID: "sales.order.cancel", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "sale.order", Method: "action_cancel",
		Verification: VerificationSpec{Field: "state", Expect: "cancel"},
	},
	{
		// sale.order uses action_draft; button_draft does not exist on this model.
		SemanticID: "sales.order.draft", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"sale"},
		RequiredCapability: "sales.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "sale.order", Method: "action_draft",
		Verification: VerificationSpec{Field: "state", Expect: "draft"},
	},
	{
		SemanticID: "procurement.order.cancel", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order", Method: "button_cancel",
		Verification: VerificationSpec{Field: "state", Expect: "cancel"},
	},
	{
		// purchase.order uses button_draft; action_draft does not exist on this model.
		SemanticID: "procurement.order.draft", Kind: KindAction, Strategy: StrategyMethodCall,
		RequiredModules:    []string{"purchase"},
		RequiredCapability: "procurement.order.write", Risk: RiskMedium, Confidence: "LIVE_WORKFLOW_VERIFIED",
		ProviderModel: "purchase.order", Method: "button_draft",
		Verification: VerificationSpec{Field: "state", Expect: "draft"},
	},
	{
		SemanticID: "admin.module.install", Kind: KindAction, Strategy: StrategyModuleInstall,
		RequiredModules:    []string{"base"},
		RequiredCapability: "governance.providermodule.administer", Risk: RiskHigh, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "ir.module.module", Method: "button_immediate_install",
		Verification:                  VerificationSpec{Field: "state", Expect: "installed"},
		PostconditionVerifyOperation:  "structure.module.list",
		PostconditionComparison:       "STATE_IN",
		PostconditionObservedPath:     "state",
		PostconditionExpectedSource:   "FROM_CONSTANT",
		PostconditionExpectedConstant: "installed",
		KnownSideEffects:              []string{"triggers immediate module installation and schema upgrades in Odoo runtime; blocks worker threads during installation"},
	},
	{
		SemanticID: "structure.location.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		// Note: added fields location_id and parent_path are SOURCE_DISCOVERED additions NOT covered by previous live evidence.
		ProviderModel: "stock.location", ReadFields: []string{"id", "complete_name", "usage", "company_id", "active", "location_id", "parent_path"},
		DefaultSort: "id asc",
	},
	{
		SemanticID: "structure.module.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"base"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "ir.module.module", ReadFields: []string{"id", "name", "state", "shortdesc"},
	},
	{
		SemanticID: "structure.website.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"website"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "website", ReadFields: []string{"id", "name", "domain", "company_id", "default_lang_id"},
	},
	{
		SemanticID: "website.menu.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"website"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "website.menu", ReadFields: []string{"id", "name", "url", "website_id", "parent_id", "sequence"},
	},
	{
		SemanticID: "website.page.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"website"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "website.page", ReadFields: []string{"id", "name", "url", "is_published", "website_id"},
	},
	{
		SemanticID: "website.site.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"website"},
		RequiredCapability: "governance.providerbinding.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "website", ReadFields: []string{"id", "name", "domain", "company_id", "default_lang_id"},
	},
	{
		SemanticID: "workforce.attendance.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"hr_attendance"},
		RequiredCapability: "workforce.schedule.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		// resource_id removed: field does not exist on resource.calendar.attendance in Odoo 19 (V2_16 F2).
		ProviderModel: "resource.calendar.attendance", ReadFields: []string{"id", "calendar_id", "dayofweek", "hour_from", "hour_to", "day_period"},
	},
	{
		SemanticID: "workforce.calendar.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"calendar"},
		RequiredCapability: "workforce.schedule.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "resource.calendar", ReadFields: []string{"id", "name", "tz", "attendance_ids"},
	},
	{
		SemanticID: "workforce.leave.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"hr_holidays"},
		RequiredCapability: "workforce.schedule.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		ProviderModel: "hr.leave", ReadFields: []string{"id", "employee_id", "date_from", "date_to", "state", "holiday_status_id"},
	},
	{
		SemanticID: "inventory.quant.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		// Note: stock.quant projection verified against official Odoo 19 source; NOT covered by previous live evidence.
		ProviderModel: "stock.quant",
		ReadFields:    []string{"id", "product_id", "location_id", "quantity", "reserved_quantity", "product_uom_id", "company_id"},
		DefaultSort:   "id asc",
	},
	{
		SemanticID: "inventory.move_line.list", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"stock"},
		RequiredCapability: "inventory.transfer.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		// Note: native provider model confirmed as stock.move.line in official Odoo 19 source (not stock.move_line);
		// NOT covered by previous live evidence. No deprecated reserved_uom_qty.
		ProviderModel: "stock.move.line",
		ReadFields:    []string{"id", "move_id", "picking_id", "product_id", "location_id", "quantity", "quantity_product_uom", "product_uom_id", "company_id", "state", "picked"},
		DefaultSort:   "id asc",
	},
	{
		SemanticID: "catalog.uom.describe", Kind: KindRead, Strategy: StrategyRead,
		RequiredModules:    []string{"uom"},
		RequiredCapability: "catalog.product.read", Risk: RiskLow, Confidence: "SOURCE_DISCOVERED",
		// Note: uom.uom fields verified against official Odoo 19 source; NOT covered by previous live evidence.
		ProviderModel: "uom.uom",
		ReadFields:    []string{"id", "name", "rounding", "active"},
		DefaultSort:   "id asc",
	},
}

// CatalogByID indexes the one authoritative odoo19Catalog for O(1) lookup.
var CatalogByID map[string]Odoo19Operation

func validateRequiredModules(operation Odoo19Operation) {
	if len(operation.RequiredModules) == 0 {
		panic("odoo: operation " + operation.SemanticID + " has no required Odoo module")
	}
	previous := ""
	for index, module := range operation.RequiredModules {
		if module == "" || strings.TrimSpace(module) != module {
			panic("odoo: operation " + operation.SemanticID + " has an invalid required Odoo module")
		}
		if index > 0 && module <= previous {
			panic("odoo: operation " + operation.SemanticID + " required Odoo modules must be sorted and unique")
		}
		previous = module
	}
}

func init() {
	CatalogByID = make(map[string]Odoo19Operation, len(odoo19Catalog))
	for i := range odoo19Catalog {
		validateRequiredModules(odoo19Catalog[i])
		// All currently governed semantics apply to all three declared
		// surfaces. This declaration is intentionally data owned by the core;
		// no direct CLI commands are enabled by it.
		odoo19Catalog[i].Applicability = allSurfaces()
		CatalogByID[odoo19Catalog[i].SemanticID] = odoo19Catalog[i]
	}
}

// Catalog returns a snapshot of the single authoritative catalog, avoiding
// caller mutation of the core's backing slice.
func Catalog() []Odoo19Operation {
	out := make([]Odoo19Operation, len(odoo19Catalog))
	copy(out, odoo19Catalog)
	return out
}

func CatalogOperationCount() int { return len(odoo19Catalog) }

func StrategyFor(operationID string) (ExecutionStrategy, bool) {
	op, ok := CatalogByID[operationID]
	return op.Strategy, ok
}

func HasImplementation(strategy ExecutionStrategy) bool {
	switch strategy {
	case StrategyRead, StrategyFieldWrite, StrategyMethodCall, StrategyWizard, StrategyDestructiveGuarded, StrategyReport:
		return true
	default:
		return false
	}
}

func HasVerification(operationID string) bool {
	op, ok := CatalogByID[operationID]
	return ok && (op.Verification.Field != "" || len(op.AdditionalVerifications) > 0)
}
