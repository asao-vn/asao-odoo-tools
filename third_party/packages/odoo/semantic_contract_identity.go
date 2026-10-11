package odoo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
)

var (
	semanticContractIdentityOnce   sync.Once
	cachedSemanticContractIdentity string
)

// canonicalSemanticField captures the contract-relevant fields of a SemanticField.
// Excluded: Description.
type canonicalSemanticField struct {
	Name           string   `json:"name"`
	ProviderField  string   `json:"provider_field"`
	Type           string   `json:"type"`
	Required       bool     `json:"required"`
	ProviderNative bool     `json:"provider_native"`
	EnumValues     []string `json:"enum_values"`
}

// canonicalOperationContract captures the contract-relevant fields of an OperationContract.
type canonicalOperationContract struct {
	Inputs  []canonicalSemanticField `json:"inputs"`
	Outputs []canonicalSemanticField `json:"outputs"`
}

// canonicalContractOperation captures exactly the contract-relevant fields of an Odoo19Operation
// and its associated OperationContract.
// Excluded: Confidence, KnownSideEffects, SemanticField.Description.
// OptionalReadFields IS included (as a canonical set): it decides which fields an operation may
// read when the provider declares them, so a client and a runtime that disagree about it do not
// share a contract. Its omission was a real freshness gap found by V2.5 Gate #3.
type canonicalContractOperation struct {
	SemanticID                  string                     `json:"semantic_id"`
	Kind                        SemanticKind               `json:"kind"`
	Strategy                    ExecutionStrategy          `json:"strategy"`
	RequiredCapability          Capability                 `json:"required_capability"`
	Risk                        RiskClassification         `json:"risk"`
	Applicability               SurfaceApplicability       `json:"applicability"`
	RequiredModules             []string                   `json:"required_modules"`
	ProviderModel               string                     `json:"provider_model"`
	ReadFields                  []string                   `json:"read_fields"`
	OptionalReadFields          []string                   `json:"optional_read_fields"`
	DefaultSort                 string                     `json:"default_sort"`
	ProviderDomain              []interface{}              `json:"provider_domain"`
	WriteMode                   WriteMode                  `json:"write_mode"`
	AllowedFields               []string                   `json:"allowed_fields"`
	ForcedValues                map[string]interface{}     `json:"forced_values"`
	FieldAliasesJSON            string                     `json:"field_aliases_json"`
	Method                      string                     `json:"method"`
	MethodArgs                  []interface{}              `json:"method_args"`
	WizardModel                 string                     `json:"wizard_model"`
	WizardLinkField             string                     `json:"wizard_link_field"`
	WizardLinkMulti             bool                       `json:"wizard_link_multi"`
	WizardMethod                string                     `json:"wizard_method"`
	WizardValues                map[string]interface{}     `json:"wizard_values"`
	WizardLinkSourceModel       string                     `json:"wizard_link_source_model"`
	WizardLinkSourceDomainField string                     `json:"wizard_link_source_domain_field"`
	WizardLinkSourceExtraDomain [][]interface{}            `json:"wizard_link_source_extra_domain"`
	ImpactModel                 string                     `json:"impact_model"`
	ImpactField                 string                     `json:"impact_field"`
	AdditionalImpacts           []ImpactSpec               `json:"additional_impacts"`
	Relations                   map[string]RelationSpec    `json:"relations"`
	Precondition                VerificationSpec           `json:"precondition"`
	Verification                VerificationSpec           `json:"verification"`
	AdditionalVerifications     []VerificationSpec         `json:"additional_verifications"`
	Contract                    canonicalOperationContract `json:"contract"`
}

// canonicalStringSet normalizes a declaration whose meaning is SET membership rather than order:
// it sorts, removes duplicates, and collapses empty to nil so that an absent declaration and an
// empty one hash identically.
//
// This deliberately differs from how ReadFields is treated. ReadFields is an ORDERED projection --
// the order in which a caller receives columns is part of that contract -- so it is hashed as
// declared. OptionalReadFields is consumed purely as a membership test in execRead
// (`if existing[opt]`), so reordering it or repeating an entry changes no behaviour and must not
// change the identity, while adding or removing a field changes what the operation may read and
// must change it.
func canonicalStringSet(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func toCanonicalSemanticFields(fields []SemanticField) []canonicalSemanticField {
	if fields == nil {
		return nil
	}
	out := make([]canonicalSemanticField, len(fields))
	for i, f := range fields {
		out[i] = canonicalSemanticField{
			Name:           f.Name,
			ProviderField:  f.ProviderField,
			Type:           f.Type,
			Required:       f.Required,
			ProviderNative: f.ProviderNative,
			EnumValues:     f.EnumValues,
		}
	}
	return out
}

func toCanonicalContract(contract OperationContract) canonicalOperationContract {
	return canonicalOperationContract{
		Inputs:  toCanonicalSemanticFields(contract.Inputs),
		Outputs: toCanonicalSemanticFields(contract.Outputs),
	}
}

func toCanonicalOperation(op Odoo19Operation, contract OperationContract) canonicalContractOperation {
	return canonicalContractOperation{
		SemanticID:                  op.SemanticID,
		Kind:                        op.Kind,
		Strategy:                    op.Strategy,
		RequiredCapability:          op.RequiredCapability,
		Risk:                        op.Risk,
		Applicability:               op.Applicability,
		RequiredModules:             canonicalStringSet(op.RequiredModules),
		ProviderModel:               op.ProviderModel,
		ReadFields:                  op.ReadFields,
		OptionalReadFields:          canonicalStringSet(op.OptionalReadFields),
		DefaultSort:                 op.DefaultSort,
		ProviderDomain:              op.ProviderDomain,
		WriteMode:                   op.WriteMode,
		AllowedFields:               op.AllowedFields,
		ForcedValues:                op.ForcedValues,
		FieldAliasesJSON:            op.FieldAliasesJSON,
		Method:                      op.Method,
		MethodArgs:                  op.MethodArgs,
		WizardModel:                 op.WizardModel,
		WizardLinkField:             op.WizardLinkField,
		WizardLinkMulti:             op.WizardLinkMulti,
		WizardMethod:                op.WizardMethod,
		WizardValues:                op.WizardValues,
		WizardLinkSourceModel:       op.WizardLinkSourceModel,
		WizardLinkSourceDomainField: op.WizardLinkSourceDomainField,
		WizardLinkSourceExtraDomain: op.WizardLinkSourceExtraDomain,
		ImpactModel:                 op.ImpactModel,
		ImpactField:                 op.ImpactField,
		AdditionalImpacts:           op.AdditionalImpacts,
		Relations:                   op.Relations,
		Precondition:                op.Precondition,
		Verification:                op.Verification,
		AdditionalVerifications:     op.AdditionalVerifications,
		Contract:                    toCanonicalContract(contract),
	}
}

// ComputeSemanticContractIdentityFromOperationsAndContracts calculates the sha256 contract identity
// over the supplied list of operations and their contracts.
func ComputeSemanticContractIdentityFromOperationsAndContracts(ops []Odoo19Operation, contracts map[string]OperationContract) string {
	if len(ops) == 0 {
		return ""
	}

	// Make a copy and sort by SemanticID to ensure declaration order does not matter.
	sortedOps := make([]Odoo19Operation, len(ops))
	copy(sortedOps, ops)
	sort.Slice(sortedOps, func(i, j int) bool {
		return sortedOps[i].SemanticID < sortedOps[j].SemanticID
	})

	canonicalOps := make([]canonicalContractOperation, len(sortedOps))
	for i, op := range sortedOps {
		var contract OperationContract
		if contracts != nil {
			contract = contracts[op.SemanticID]
		}
		canonicalOps[i] = toCanonicalOperation(op, contract)
	}

	payload, err := json.Marshal(canonicalOps)
	if err != nil {
		panic("odoo: failed to marshal canonical contract operations: " + err.Error())
	}

	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// ComputeSemanticContractIdentityFromOperations calculates the sha256 contract identity
// over the supplied list of operations using the default Odoo19Contracts map.
func ComputeSemanticContractIdentityFromOperations(ops []Odoo19Operation) string {
	return ComputeSemanticContractIdentityFromOperationsAndContracts(ops, Odoo19Contracts)
}

// ComputeSemanticContractIdentity returns a sha256 over the canonical serialization of
// every operation's contract-relevant declaration including its contract schema.
func ComputeSemanticContractIdentity() string {
	semanticContractIdentityOnce.Do(func() {
		cachedSemanticContractIdentity = ComputeSemanticContractIdentityFromOperationsAndContracts(Catalog(), Odoo19Contracts)
	})
	return cachedSemanticContractIdentity
}
