package odoo

// normalizeRecords rewrites a provider READ payload into the operation's
// semantic output shape.
//
// Odoo emits two conventions that an agent otherwise has to learn the hard
// way: an unset value of any type comes back as the boolean false, and a
// many2one comes back as a positional [id, display_name] pair. Both are
// meaningful to Odoo and meaningless as a stable contract, so they are
// translated here — once, generically, driven by the contract table — rather
// than in per-operation code or in every consumer.
func normalizeRecords(semanticID string, raw interface{}) interface{} {
	contract, ok := Odoo19Contracts[semanticID]
	if !ok || len(contract.Outputs) == 0 {
		return raw
	}
	switch v := raw.(type) {
	case []map[string]interface{}:
		// The transport's actual SearchRead return type. Missing this case
		// silently passed every read through unnormalized.
		out := make([]interface{}, 0, len(v))
		for _, rec := range v {
			out = append(out, normalizeRecord(contract, rec))
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(v))
		for _, rec := range v {
			out = append(out, normalizeRecord(contract, rec))
		}
		return out
	case map[string]interface{}:
		return normalizeRecord(contract, v)
	default:
		return raw
	}
}

func normalizeRecord(contract OperationContract, rec interface{}) interface{} {
	m, ok := rec.(map[string]interface{})
	if !ok {
		return rec
	}
	out := make(map[string]interface{}, len(m))
	for providerField, value := range m {
		spec, known := contract.OutputByProviderField(providerField)
		if !known {
			// Unmapped provider fields pass through untouched rather than
			// being dropped: losing data would be worse than leaving one
			// field provider-shaped.
			out[providerField] = value
			continue
		}
		out[spec.Name] = normalizeValue(spec, value)
	}
	return out
}

func normalizeValue(spec SemanticField, value interface{}) interface{} {
	// Odoo's "absent" is `false` regardless of the field's real type. Only
	// collapse it to null where the declared type is not itself boolean,
	// otherwise a legitimately false flag would vanish.
	if b, isBool := value.(bool); isBool && !b && spec.Type != "boolean" {
		return nil
	}
	// A closed-value field is reported in the same semantic vocabulary the
	// input contract accepts, so a value an agent reads is a value it can
	// write back. Previously a read returned Odoo's own spelling (state
	// "1_done") while help documented nothing, which is how the provider's
	// ordinal-prefixed enum leaked out as the de-facto public API.
	if spec.IsEnum() {
		if s, ok := value.(string); ok {
			return spec.FromProviderValue(s)
		}
	}
	switch spec.Type {
	case "reference":
		if pair, ok := value.([]interface{}); ok && len(pair) == 2 {
			return map[string]interface{}{"id": pair[0], "display_name": pair[1]}
		}
	case "reference_list":
		if list, ok := value.([]interface{}); ok {
			refs := make([]interface{}, 0, len(list))
			for _, item := range list {
				if pair, ok := item.([]interface{}); ok && len(pair) == 2 {
					refs = append(refs, map[string]interface{}{"id": pair[0], "display_name": pair[1]})
					continue
				}
				refs = append(refs, map[string]interface{}{"id": item})
			}
			return refs
		}
	}
	return value
}
