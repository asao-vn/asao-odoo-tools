package direct

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrFieldsModelRequired      = errors.New("odoo direct: model is required")
	ErrFieldsAttributeBlank     = errors.New("odoo direct: attribute cannot be blank")
	ErrFieldsAttributeDuplicate = errors.New("odoo direct: duplicate attribute")
)

var sensitiveFieldMetadataKeyFragments = []string{
	"password",
	"secret",
	"token",
	"credential",
	"apikey",
	"privatekey",
	"cookie",
	"authorization",
}

// DefaultFieldAttributes represents the bounded default attribute set queried when none are supplied.
var DefaultFieldAttributes = []string{
	"string",
	"type",
	"required",
	"readonly",
	"relation",
	"selection",
}

// FieldMetadata holds sorted discovery metadata for a single model field.
type FieldMetadata struct {
	Name       string                 `json:"name"`
	Attributes map[string]interface{} `json:"attributes"`
}

// FieldsResult represents the unmediated live provider schema discovery outcome.
// It carries raw: true, discovery: "provider_schema", and explicitly declares that
// it makes no semantic readiness or evidence claim.
type FieldsResult struct {
	Raw            bool            `json:"raw"`
	Discovery      string          `json:"discovery"`
	Model          string          `json:"model"`
	Attributes     []string        `json:"attributes"`
	Fields         []FieldMetadata `json:"fields"`
	SchemaHash     string          `json:"schemaHash"`
	ProviderRoute  ProviderRoute   `json:"providerRoute"`
	ReadinessClaim string          `json:"readinessClaim"`
	EvidenceClaim  string          `json:"evidenceClaim"`
	Notice         string          `json:"notice,omitempty"`
}

// ValidateFieldAttributes normalizes, validates, and sorts the requested attribute set.
// It rejects blank and duplicate attributes before provider contact. If attributes is empty,
// it returns a copy of DefaultFieldAttributes.
func ValidateFieldAttributes(attributes []string) ([]string, error) {
	if len(attributes) == 0 {
		clean := append([]string(nil), DefaultFieldAttributes...)
		sort.Strings(clean)
		return clean, nil
	}
	seen := make(map[string]bool, len(attributes))
	clean := make([]string, 0, len(attributes))
	for _, attr := range attributes {
		trimmed := strings.TrimSpace(attr)
		if trimmed == "" {
			return nil, ErrFieldsAttributeBlank
		}
		lower := strings.ToLower(trimmed)
		if seen[lower] {
			return nil, fmt.Errorf("%w: %q", ErrFieldsAttributeDuplicate, trimmed)
		}
		seen[lower] = true
		clean = append(clean, trimmed)
	}
	sort.Strings(clean)
	return clean, nil
}

// ComputeSchemaHash returns the hex-encoded SHA-256 digest of the canonical JSON
// serialization of the sorted field metadata.
func ComputeSchemaHash(fields []FieldMetadata) (string, error) {
	canonicalJSON, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("odoo direct: could not marshal fields for schema hash: %w", err)
	}
	sum := sha256.Sum256(canonicalJSON)
	return hex.EncodeToString(sum[:]), nil
}

func isSensitiveFieldMetadataKey(key string) bool {
	var normalized strings.Builder
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
		}
	}
	clean := normalized.String()
	for _, fragment := range sensitiveFieldMetadataKeyFragments {
		if strings.Contains(clean, fragment) {
			return true
		}
	}
	return false
}

// sanitizeFieldMetadataValue keeps safe provider metadata while removing
// secret-looking keys from arbitrary JSON-like nested values. The provider
// owns fields_get output, so raw schema discovery must not echo credentials
// into CLI output or schema hashes.
func sanitizeFieldMetadataValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		clean := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			if isSensitiveFieldMetadataKey(key) {
				continue
			}
			clean[key] = sanitizeFieldMetadataValue(nested)
		}
		return clean
	case []interface{}:
		clean := make([]interface{}, len(typed))
		for index, nested := range typed {
			clean[index] = sanitizeFieldMetadataValue(nested)
		}
		return clean
	default:
		return value
	}
}

// FieldsGet queries live model field metadata using Odoo's read-only fields_get RPC method.
// It rejects blank model names and blank or duplicate attributes before contacting the provider.
// The response is sorted deterministically and sealed with a SHA-256 schema hash.
func (e *Executor) FieldsGet(ctx context.Context, model string, attributes []string) (FieldsResult, error) {
	if e == nil || e.client == nil {
		return FieldsResult{}, errors.New("odoo direct: executor is not initialized")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return FieldsResult{}, ErrFieldsModelRequired
	}
	cleanAttributes, err := ValidateFieldAttributes(attributes)
	if err != nil {
		return FieldsResult{}, err
	}

	args := []interface{}{}
	kwargs := map[string]interface{}{
		"attributes": cleanAttributes,
	}
	res, err := e.client.ExecuteKw(ctx, model, "fields_get", args, kwargs)
	if err != nil {
		return FieldsResult{}, classifyRawExecutionError(model, "fields_get", err)
	}

	rawFields, ok := res.(map[string]interface{})
	if !ok {
		return FieldsResult{}, fmt.Errorf("odoo direct: unexpected fields_get response type %T", res)
	}

	names := make([]string, 0, len(rawFields))
	for name := range rawFields {
		names = append(names, name)
	}
	sort.Strings(names)

	fields := make([]FieldMetadata, 0, len(names))
	for _, name := range names {
		attrMap, _ := rawFields[name].(map[string]interface{})
		cleanAttr := make(map[string]interface{}, len(attrMap))
		for k, v := range attrMap {
			if isSensitiveFieldMetadataKey(k) {
				continue
			}
			cleanAttr[k] = sanitizeFieldMetadataValue(v)
		}
		fields = append(fields, FieldMetadata{
			Name:       name,
			Attributes: cleanAttr,
		})
	}

	schemaHash, err := ComputeSchemaHash(fields)
	if err != nil {
		return FieldsResult{}, err
	}

	return FieldsResult{
		Raw:            true,
		Discovery:      "provider_schema",
		Model:          model,
		Attributes:     cleanAttributes,
		Fields:         fields,
		SchemaHash:     schemaHash,
		ProviderRoute:  e.route,
		ReadinessClaim: "NONE",
		EvidenceClaim:  "NONE",
		Notice:         "Live provider schema discovery only; carries no semantic readiness or evidence claim.",
	}, nil
}
