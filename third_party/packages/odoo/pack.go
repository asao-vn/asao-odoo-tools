package odoo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Operation packs (DEC-2026-0176).
//
// A pack declares a custom module's own operations as data: an id, a kind, a model, fields, a button
// method, a wizard or a report, and a risk. Nothing in it is code. RegisterPack turns each declaration
// into an operation of the standalone catalog, second class like the surface catalog's:
//
//   - it is STANDALONE-ONLY: lookupOperation finds it only for the standalone executor, and nothing
//     governed lists, describes, authorizes or routes it;
//   - its evidence is PACK_DECLARED and its readiness UNVERIFIED: no conformance manifest names it;
//   - its fields are provider-native, read as OptionalReadFields.
//
// What this file checks needs no Odoo: names, kinds, the models and fields a pack may never touch,
// risk floors, and collisions with the built-in catalogs. Whether a pack's models, fields, buttons,
// wizards and reports exist is checked live when a person syncs or adds it.

// PackSchema names the pack format this build reads.
const PackSchema = "odoo-cli.pack/v1"

// PackCustomModule is the module of a pack for customizations made in the database itself (Settings,
// Technical, or Studio), which no installed module owns. Such a pack is added by hand and requires
// only base.
const PackCustomModule = "custom"

// Odoo19EvidencePackDeclared labels an operation a pack declares: nothing about it has been verified.
const Odoo19EvidencePackDeclared Odoo19EvidenceClass = "PACK_DECLARED"

// CredentialModelPrefixes are the models that hold credentials or system configuration. explore never
// reads them, and a pack never names them.
var CredentialModelPrefixes = []string{
	"ir.config_parameter", "ir.mail_server", "fetchmail.server", "res.users.apikeys", "res.users.identitycheck",
	"res.users.deletion", "payment.provider", "auth.oauth.provider", "auth_totp", "iap.account", "certificate.",
	"account.online.", "ir.attachment", "ir.module.module", "ir.cron", "ir.actions", "ir.rule", "ir.model.access",
	// Odoo 20 merges ir.model.access and ir.rule into ir.access: the same access configuration, refused
	// the same way.
	"ir.access",
}

// SecretFieldWords mark, by its name, a field that holds a secret.
var SecretFieldWords = []string{"password", "passwd", "secret", "token", "api_key", "apikey", "private_key", "totp", "credential"}

// IsCredentialModel reports whether a model holds credentials or system configuration.
func IsCredentialModel(model string) bool {
	for _, prefix := range CredentialModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// IsSecretField reports whether a field's name marks a secret.
func IsSecretField(name string) bool {
	lowered := strings.ToLower(name)
	for _, word := range SecretFieldWords {
		if strings.Contains(lowered, word) {
			return true
		}
	}
	return false
}

// packRefusedModelPrefixes are refused besides the credential models: the whole technical layer,
// who signs in and with which rights, company-wide settings, the models that run code, and the mail
// a write would send or the template it would render.
var packRefusedModelPrefixes = []string{"ir.", "res.users", "res.groups", "res.company", "res.config", "base.", "mail.mail", "mail.template"}

// packMoneyModels move money or say who is paid: an operation on one is HIGH risk at least.
var packMoneyModels = map[string]bool{
	"res.partner.bank": true, "account.payment": true, "account.move": true, "account.move.line": true,
	"account.bank.statement.line": true,
}

// packRefusedMethods are the ORM's own methods: a pack names a button of the module, never a generic
// write, delete or read under another name.
var packRefusedMethods = map[string]bool{
	"create": true, "write": true, "unlink": true, "copy": true, "read": true, "search": true, "search_read": true,
	"search_count": true, "name_search": true, "name_create": true, "fields_get": true, "default_get": true,
	"export_data": true, "load": true, "get_views": true, "onchange": true, "web_save": true, "web_read": true,
	"sudo": true, "with_user": true, "with_context": true, "browse": true,
}

var (
	packModuleName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	packModelName  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)
	packFieldName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	packIDPart     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	packReportName = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[A-Za-z0-9_.]+$`)
	packSortSpec   = regexp.MustCompile(`^[a-z][a-z0-9_]*( (asc|desc))?(, ?[a-z][a-z0-9_]*( (asc|desc))?)*$`)
)

// Pack is one module's declared operations.
type Pack struct {
	Schema     string          `json:"schema"`
	Module     string          `json:"module"`
	Title      string          `json:"title,omitempty"`
	Author     string          `json:"author,omitempty"`
	Series     []string        `json:"series,omitempty"`
	Operations []PackOperation `json:"operations"`
}

// PackOperation is one declared operation. Which fields it uses depends on its strategy.
type PackOperation struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Strategy string `json:"strategy"`
	Model    string `json:"model"`
	Summary  string `json:"summary,omitempty"`
	Risk     string `json:"risk,omitempty"`
	// READ
	ReadFields  []string `json:"readFields,omitempty"`
	DefaultSort string   `json:"defaultSort,omitempty"`
	// FIELD_WRITE
	WriteMode string                  `json:"writeMode,omitempty"`
	Fields    []string                `json:"fields,omitempty"`
	Relations map[string]PackRelation `json:"relations,omitempty"`
	// Types say how a written value is read from the command line: string, integer, number, boolean or
	// reference (a many2one, which needs a relation). Sync fills them from Odoo's fields_get.
	Types map[string]string `json:"types,omitempty"`
	// METHOD_CALL: an object button of the model's views.
	Method string `json:"method,omitempty"`
	// WIZARD: a transient model opened on the target record, and the button that applies it.
	Wizard *PackWizard `json:"wizard,omitempty"`
	// REPORT
	Report string `json:"report,omitempty"`
}

// PackRelation lets a many2one input take a name as well as an id.
type PackRelation struct {
	Model       string `json:"model"`
	SearchField string `json:"searchField,omitempty"`
}

// PackWizard is the wizard a WIZARD operation fills and applies.
type PackWizard struct {
	Model  string   `json:"model"`
	Method string   `json:"method"`
	Fields []string `json:"fields,omitempty"`
}

// PackOrigin says where a registered operation comes from.
type PackOrigin struct {
	Module string `json:"module"`
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	// Source is where the person took the pack from: the module's static file or a file.
	Source string `json:"source,omitempty"`
}

// ParsePack reads a pack and checks everything that needs no Odoo. Unknown keys are refused, so a
// misspelt field never silently drops a declaration.
func ParsePack(data []byte) (Pack, error) {
	var pack Pack
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pack); err != nil {
		return Pack{}, fmt.Errorf("pack: not an %s document: %v", PackSchema, err)
	}
	if err := pack.Validate(nil); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

// PackModels lists the models a pack names: its operations', its wizards' and its relations'.
func (p Pack) PackModels() []string {
	seen := map[string]bool{}
	for _, op := range p.Operations {
		seen[op.Model] = true
		if op.Wizard != nil {
			seen[op.Wizard.Model] = true
		}
		for _, relation := range op.Relations {
			seen[relation.Model] = true
		}
	}
	out := make([]string, 0, len(seen))
	for model := range seen {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

// Validate checks a pack without Odoo. reserved names the module names a pack may not take besides
// the catalog's families: the command line's own commands, which a pack's commands would shadow.
func (p Pack) Validate(reserved []string) error {
	if p.Schema != PackSchema {
		return fmt.Errorf("pack: schema is %q, this build reads %q", p.Schema, PackSchema)
	}
	if !packModuleName.MatchString(p.Module) {
		return fmt.Errorf("pack: module %q is not an Odoo module name", p.Module)
	}
	if builtinFamilies()[p.Module] {
		return fmt.Errorf("pack: module %q is the name of a catalog family; the pack's commands would mix with the catalog's", p.Module)
	}
	for _, name := range reserved {
		if p.Module == name {
			return fmt.Errorf("pack: module %q is the name of a command of its own", p.Module)
		}
	}
	if len(p.Operations) == 0 {
		return fmt.Errorf("pack %s: declares no operation", p.Module)
	}
	if len(p.Operations) > 500 {
		return fmt.Errorf("pack %s: declares %d operations, at most 500", p.Module, len(p.Operations))
	}
	ids := map[string]bool{}
	for _, op := range p.Operations {
		if err := p.validateOperation(op); err != nil {
			return fmt.Errorf("pack %s: %s: %w", p.Module, op.ID, err)
		}
		if ids[op.ID] {
			return fmt.Errorf("pack %s: %s is declared twice", p.Module, op.ID)
		}
		ids[op.ID] = true
	}
	return nil
}

func (p Pack) validateOperation(op PackOperation) error {
	parts := strings.Split(op.ID, ".")
	if len(parts) < 3 || len(parts) > 5 || parts[0] != p.Module {
		return fmt.Errorf("an id is <module>.<entity>.<verb> and begins with the pack's module %q", p.Module)
	}
	for _, part := range parts {
		if !packIDPart.MatchString(part) {
			return fmt.Errorf("id part %q is not lower-case letters, digits and underscores", part)
		}
	}
	if _, ok := lookupOperation(op.ID, true); ok && !IsPackOperation(op.ID) {
		return fmt.Errorf("the id is a built-in catalog operation")
	}
	if err := packModelAllowed(op.Model); err != nil {
		return err
	}
	if op.Risk != "" && !validRisk(op.Risk) {
		return fmt.Errorf("risk %q is not LOW, MEDIUM, HIGH or CRITICAL", op.Risk)
	}
	if strings.TrimSpace(op.Summary) != op.Summary || len(op.Summary) > 300 {
		return fmt.Errorf("the summary is trimmed text of at most 300 characters")
	}
	used := map[string]bool{"readFields": len(op.ReadFields) > 0, "defaultSort": op.DefaultSort != "", "writeMode": op.WriteMode != "",
		"fields": len(op.Fields) > 0, "relations": len(op.Relations) > 0, "types": len(op.Types) > 0, "method": op.Method != "", "wizard": op.Wizard != nil, "report": op.Report != ""}
	allowed := map[string][]string{
		"READ":        {"readFields", "defaultSort"},
		"FIELD_WRITE": {"writeMode", "fields", "relations", "types"},
		"METHOD_CALL": {"method"},
		"WIZARD":      {"wizard", "relations", "types"},
		"REPORT":      {"report"},
	}
	kinds := map[string]string{"READ": "READ", "REPORT": "READ", "FIELD_WRITE": "COMMAND", "METHOD_CALL": "ACTION", "WIZARD": "ACTION"}
	keys, known := allowed[op.Strategy]
	if !known {
		return fmt.Errorf("strategy %q is not READ, FIELD_WRITE, METHOD_CALL, WIZARD or REPORT (a pack never deletes, installs or calls a method that is not a button)", op.Strategy)
	}
	if op.Kind != kinds[op.Strategy] {
		return fmt.Errorf("a %s operation is of kind %s, not %q", op.Strategy, kinds[op.Strategy], op.Kind)
	}
	for key, present := range used {
		if present && !packContains(keys, key) {
			return fmt.Errorf("a %s operation does not take %q", op.Strategy, key)
		}
	}
	switch op.Strategy {
	case "READ":
		if len(op.ReadFields) == 0 || len(op.ReadFields) > 60 {
			return fmt.Errorf("a read names 1 to 60 readFields")
		}
		if err := packFieldsAllowed(op.ReadFields); err != nil {
			return err
		}
		if op.DefaultSort != "" && !packSortSpec.MatchString(op.DefaultSort) {
			return fmt.Errorf("defaultSort %q is not field [asc|desc], ...", op.DefaultSort)
		}
	case "FIELD_WRITE":
		if op.WriteMode != string(WriteModeCreate) && op.WriteMode != string(WriteModeUpdate) {
			return fmt.Errorf("writeMode is CREATE or UPDATE")
		}
		if len(op.Fields) == 0 || len(op.Fields) > 60 {
			return fmt.Errorf("a write names 1 to 60 fields")
		}
		if err := packFieldsAllowed(op.Fields); err != nil {
			return err
		}
		if err := packRelationsAllowed(op.Relations, op.Fields); err != nil {
			return err
		}
		if err := packTypesAllowed(op.Types, op.Relations, op.Fields); err != nil {
			return err
		}
	case "METHOD_CALL":
		if err := packMethodAllowed(op.Method); err != nil {
			return err
		}
	case "WIZARD":
		if op.Wizard.Model == op.Model {
			return fmt.Errorf("the wizard is a model of its own")
		}
		if err := packModelAllowed(op.Wizard.Model); err != nil {
			return fmt.Errorf("wizard: %w", err)
		}
		if err := packMethodAllowed(op.Wizard.Method); err != nil {
			return fmt.Errorf("wizard: %w", err)
		}
		if len(op.Wizard.Fields) > 60 {
			return fmt.Errorf("a wizard takes at most 60 fields")
		}
		if err := packFieldsAllowed(op.Wizard.Fields); err != nil {
			return fmt.Errorf("wizard: %w", err)
		}
		if err := packRelationsAllowed(op.Relations, op.Wizard.Fields); err != nil {
			return err
		}
		if err := packTypesAllowed(op.Types, op.Relations, op.Wizard.Fields); err != nil {
			return err
		}
	case "REPORT":
		if !packReportName.MatchString(op.Report) {
			return fmt.Errorf("report %q is not <module>.<report_name>", op.Report)
		}
	}
	return nil
}

func packModelAllowed(model string) error {
	if len(model) > 128 || !packModelName.MatchString(model) {
		return fmt.Errorf("model %q is not an Odoo model name", model)
	}
	if IsCredentialModel(model) {
		return fmt.Errorf("model %s holds credentials or system configuration", model)
	}
	for _, prefix := range packRefusedModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return fmt.Errorf("model %s is refused to packs: sign-in and rights, company settings, code and mail stay with a person", model)
		}
	}
	return nil
}

func packFieldsAllowed(fields []string) error {
	seen := map[string]bool{}
	for _, field := range fields {
		if !packFieldName.MatchString(field) {
			return fmt.Errorf("field %q is not a plain field name", field)
		}
		if IsSecretField(field) {
			return fmt.Errorf("field %s holds a secret by its name", field)
		}
		if seen[field] {
			return fmt.Errorf("field %s is named twice", field)
		}
		seen[field] = true
	}
	return nil
}

func packRelationsAllowed(relations map[string]PackRelation, fields []string) error {
	for field, relation := range relations {
		if !packContains(fields, field) {
			return fmt.Errorf("relation %s is not one of the operation's fields", field)
		}
		if err := packModelAllowed(relation.Model); err != nil {
			return fmt.Errorf("relation %s: %w", field, err)
		}
		if relation.SearchField != "" && (!packFieldName.MatchString(relation.SearchField) || IsSecretField(relation.SearchField)) {
			return fmt.Errorf("relation %s: searchField %q is not a plain field", field, relation.SearchField)
		}
	}
	return nil
}

// PackInputTypes are the types a pack gives a written field.
var PackInputTypes = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true, "reference": true}

func packTypesAllowed(types map[string]string, relations map[string]PackRelation, fields []string) error {
	for field, kind := range types {
		if !packContains(fields, field) {
			return fmt.Errorf("type of %s: not one of the operation's fields", field)
		}
		if !PackInputTypes[kind] {
			return fmt.Errorf("type of %s is %q, not string, integer, number, boolean or reference", field, kind)
		}
		if _, related := relations[field]; kind == "reference" && !related {
			return fmt.Errorf("%s is a reference and needs a relation", field)
		}
	}
	return nil
}

func packMethodAllowed(method string) error {
	if !packFieldName.MatchString(method) {
		return fmt.Errorf("method %q is not a public method name (one that begins with _ is private)", method)
	}
	if packRefusedMethods[method] {
		return fmt.Errorf("method %s is the ORM's own, not a button of the module", method)
	}
	return nil
}

func validRisk(risk string) bool {
	switch RiskClassification(risk) {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	}
	return false
}

var riskOrder = map[RiskClassification]int{RiskLow: 0, RiskMedium: 1, RiskHigh: 2, RiskCritical: 3}

// PackRisk is a declared operation's risk once the floors apply: a declaration may raise a risk,
// never lower it under them. A button or a wizard is MEDIUM at least; anything that touches a model
// that moves money or says who is paid is HIGH at least.
func PackRisk(op PackOperation) RiskClassification {
	risk := RiskLow
	if op.Risk != "" {
		risk = RiskClassification(op.Risk)
	}
	floor := RiskLow
	if op.Strategy == "METHOD_CALL" || op.Strategy == "WIZARD" {
		floor = RiskMedium
	}
	if op.Strategy != "READ" && op.Strategy != "REPORT" {
		models := []string{op.Model}
		if op.Wizard != nil {
			models = append(models, op.Wizard.Model)
		}
		for _, model := range models {
			if packMoneyModels[model] {
				floor = RiskHigh
			}
		}
	}
	if riskOrder[floor] > riskOrder[risk] {
		return floor
	}
	return risk
}

// operation turns a declaration into a catalog operation.
func (p Pack) operation(op PackOperation) Odoo19Operation {
	required := []string{p.Module}
	if p.Module == PackCustomModule {
		required = []string{"base"}
	}
	access := "write"
	if op.Strategy == "READ" || op.Strategy == "REPORT" {
		access = "read"
	}
	out := Odoo19Operation{
		SemanticID:         op.ID,
		Kind:               SemanticKind(op.Kind),
		Strategy:           ExecutionStrategy(op.Strategy),
		RequiredCapability: Capability("pack." + p.Module + "." + access),
		Risk:               PackRisk(op),
		Confidence:         string(Odoo19EvidencePackDeclared),
		RequiredModules:    required,
		ProviderModel:      op.Model,
		Applicability: SurfaceApplicability{
			DirectCLI:     true,
			AERPCLI:       false,
			AERPCLIReason: "pack-declared (DEC-2026-0176): a third party's declaration, never governed",
			Conformance:   true,
		},
	}
	relations := func(fields []string) {
		for _, field := range fields {
			if kind := op.Types[field]; kind != "" && kind != "string" {
				if out.InputTypes == nil {
					out.InputTypes = map[string]string{}
				}
				out.InputTypes[field] = kind
			}
			relation, ok := op.Relations[field]
			if !ok {
				continue
			}
			if out.InputTypes == nil {
				out.InputTypes = map[string]string{}
			}
			if out.Relations == nil {
				out.Relations = map[string]RelationSpec{}
			}
			out.InputTypes[field] = "reference"
			out.Relations[field] = RelationSpec{TargetModel: relation.Model, SearchField: relation.SearchField}
		}
	}
	switch op.Strategy {
	case "READ":
		out.OptionalReadFields = append([]string(nil), op.ReadFields...)
		out.DefaultSort = op.DefaultSort
	case "FIELD_WRITE":
		out.WriteMode = WriteMode(op.WriteMode)
		out.AllowedFields = append([]string(nil), op.Fields...)
		relations(op.Fields)
	case "METHOD_CALL":
		out.Method = op.Method
	case "WIZARD":
		out.WizardModel = op.Wizard.Model
		out.WizardMethod = op.Wizard.Method
		out.WizardActiveContext = true
		out.WizardAllowedFields = append([]string(nil), op.Wizard.Fields...)
		relations(op.Wizard.Fields)
	case "REPORT":
		out.ReportName = op.Report
	}
	return out
}

var (
	packMutex       sync.RWMutex
	packCatalogByID = map[string]Odoo19Operation{}
	packOrigins     = map[string]PackOrigin{}
	packSummaries   = map[string]string{}
)

// RegisterPack adds a pack's operations to the standalone catalog. It refuses the whole pack when
// one operation fails a check or collides with an operation already known, built in or from
// another pack.
func RegisterPack(pack Pack, origin PackOrigin, reserved []string) error {
	if err := pack.Validate(reserved); err != nil {
		return err
	}
	origin.Module = pack.Module
	if origin.Title == "" {
		origin.Title = pack.Title
	}
	if origin.Author == "" {
		origin.Author = pack.Author
	}
	packMutex.Lock()
	defer packMutex.Unlock()
	for _, op := range pack.Operations {
		if existing, taken := packOrigins[op.ID]; taken {
			return fmt.Errorf("pack %s: %s is already declared by pack %s", pack.Module, op.ID, existing.Module)
		}
	}
	for _, op := range pack.Operations {
		packCatalogByID[op.ID] = pack.operation(op)
		packOrigins[op.ID] = origin
		packSummaries[op.ID] = op.Summary
	}
	return nil
}

// ForgetPacks removes every registered pack, for a test or a reload.
func ForgetPacks() {
	packMutex.Lock()
	defer packMutex.Unlock()
	packCatalogByID = map[string]Odoo19Operation{}
	packOrigins = map[string]PackOrigin{}
	packSummaries = map[string]string{}
}

// PackCatalog returns the registered pack operations, sorted by id.
func PackCatalog() []Odoo19Operation {
	packMutex.RLock()
	defer packMutex.RUnlock()
	out := make([]Odoo19Operation, 0, len(packCatalogByID))
	for _, op := range packCatalogByID {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SemanticID < out[j].SemanticID })
	return out
}

// IsPackOperation reports whether id names a registered pack operation.
func IsPackOperation(id string) bool {
	packMutex.RLock()
	defer packMutex.RUnlock()
	_, ok := packCatalogByID[id]
	return ok
}

// PackOriginOf says which pack declared an operation.
func PackOriginOf(id string) (PackOrigin, bool) {
	packMutex.RLock()
	defer packMutex.RUnlock()
	origin, ok := packOrigins[id]
	return origin, ok
}

// PackSummaryOf is the summary the pack gives an operation.
func PackSummaryOf(id string) string {
	packMutex.RLock()
	defer packMutex.RUnlock()
	return packSummaries[id]
}

func lookupPackOperation(id string) (Odoo19Operation, bool) {
	packMutex.RLock()
	defer packMutex.RUnlock()
	op, ok := packCatalogByID[id]
	return op, ok
}

// builtinFamilies are the first parts of the built-in operation ids (sales, finance, identity...).
func builtinFamilies() map[string]bool {
	families := map[string]bool{}
	for id := range CatalogByID {
		family, _, _ := strings.Cut(id, ".")
		families[family] = true
	}
	for id := range SurfaceCatalogByID {
		family, _, _ := strings.Cut(id, ".")
		families[family] = true
	}
	return families
}

func packContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
