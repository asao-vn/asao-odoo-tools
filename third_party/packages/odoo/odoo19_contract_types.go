package odoo

// SemanticField is one consumer-facing field of an operation's contract,
// bound to the Odoo19 field it actually reads or writes.
//
// The binding is what lets an agent say `--stock-managed true` without
// knowing that Odoo 19 calls it is_storable, and lets a read come back as
// `"manager": {"id": 2, "display_name": "Administrator"}` rather than the
// bare `[2, "Administrator"]` tuple the provider emits.
type SemanticField struct {
	// Name is the stable consumer-facing name. Provider version bindings may
	// map the same Name to different ProviderFields across Odoo versions.
	Name string
	// ProviderField is the Odoo19 field Name resolves to.
	ProviderField string
	// Type is the semantic type: string, integer, number, boolean,
	// reference (a related record) or reference_list.
	Type string
	// Required marks fields the provider will reject a create without.
	Required bool
	// ProviderNative is true when Name is still just the Odoo field name
	// because the concept has no researched normalization yet. It is
	// surfaced to consumers so "this is provider-specific" is explicit
	// rather than something the caller discovers by failing.
	ProviderNative bool
	Description    string

	// EnumValues is the closed set of accepted values, in SEMANTIC spelling,
	// for a selection field. Empty means the field is not an enum.
	//
	// Without this a closed-value field looked like a free string: the only
	// way to find a legal value was to guess, and guessing at a create wrote
	// junk records into the business system before the caller learned
	// anything. These values are published in --help and echoed back in the
	// rejection message, so they are discoverable without any write attempt.
	EnumValues []string
	// ProviderEnum maps a semantic value to the provider's own spelling
	// (e.g. "in-progress" -> "01_in_progress"). A semantic value with no
	// entry is passed through unchanged, so enums whose provider values are
	// already good semantic words need no mapping at all.
	ProviderEnum map[string]string
}

// IsEnum reports whether this field accepts only a closed set of values.
func (f SemanticField) IsEnum() bool { return len(f.EnumValues) > 0 }

// ToProviderValue translates a caller-supplied semantic enum value into the
// provider's spelling. ok is false when the value is not in the declared set —
// a caller error the executor reports before contacting the provider, listing
// the values it should have used.
func (f SemanticField) ToProviderValue(semantic string) (string, bool) {
	if !f.IsEnum() {
		return semantic, true
	}
	for _, v := range f.EnumValues {
		if v != semantic {
			continue
		}
		if mapped, ok := f.ProviderEnum[semantic]; ok {
			return mapped, true
		}
		return semantic, true
	}
	return "", false
}

// FromProviderValue translates a provider enum value back into its semantic
// spelling, so a value a caller reads is a value it can write back.
func (f SemanticField) FromProviderValue(provider string) string {
	for semantic, mapped := range f.ProviderEnum {
		if mapped == provider {
			return semantic
		}
	}
	return provider
}

// OperationContract is the input/output surface of one semantic operation.
type OperationContract struct {
	Inputs  []SemanticField
	Outputs []SemanticField
}

// InputByName resolves a consumer-supplied argument name to its field spec.
func (c OperationContract) InputByName(name string) (SemanticField, bool) {
	for _, f := range c.Inputs {
		if f.Name == name {
			return f, true
		}
	}
	return SemanticField{}, false
}

// InputByProviderField resolves the provider field an argument lands in back
// to its input spec, so value-level rules (enum translation) can be applied
// after name remapping without the caller's spelling mattering.
func (c OperationContract) InputByProviderField(pf string) (SemanticField, bool) {
	for _, f := range c.Inputs {
		if f.ProviderField == pf {
			return f, true
		}
	}
	return SemanticField{}, false
}

// OutputByProviderField resolves a provider field back to its semantic spec.
func (c OperationContract) OutputByProviderField(pf string) (SemanticField, bool) {
	for _, f := range c.Outputs {
		if f.ProviderField == pf {
			return f, true
		}
	}
	return SemanticField{}, false
}

// InputNames lists the accepted semantic input names, for error payloads
// that need to tell an agent what it could have said instead.
func (c OperationContract) InputNames() []string {
	out := make([]string, 0, len(c.Inputs))
	for _, f := range c.Inputs {
		out = append(out, f.Name)
	}
	return out
}
