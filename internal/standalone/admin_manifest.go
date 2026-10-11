package standalone

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// AdminConfigSchemaVersion is the required schema version for admin config manifests.
const AdminConfigSchemaVersion = "odoo.admin.config/v1"

// AdminManifest represents the versioned declarative configuration document for Odoo instance administration.
// V1 is strictly focused on declared Odoo user language preferences.
// Module installation is not supported here and remains governed exclusively through ASAO capability packs.
type AdminManifest struct {
	SchemaVersion string           `json:"schemaVersion"`
	Metadata      ManifestMetadata `json:"metadata,omitempty"`
	Users         []UserPreference `json:"users"`
}

// ManifestMetadata provides optional human-readable provenance for the manifest.
// Note: Metadata fields are caller-controlled and are never echoed back in command output to prevent secret disclosure.
type ManifestMetadata struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// UserPreference declares the target language preference for an existing Odoo user login.
type UserPreference struct {
	Login string `json:"login"`
	Lang  string `json:"lang"`
}

var (
	// langCodePattern validates standard locale codes (e.g. en_US, vi_VN, fr_FR, etc.).
	langCodePattern = regexp.MustCompile(`^[a-z]{2,3}(_[A-Za-z0-9]{2,})?$`)

	// credentialKeywords is used to recursively detect and reject credentials anywhere in the manifest.
	credentialKeywords = []string{
		"password",
		"secret",
		"token",
		"credential",
		"apikey",
		"api_key",
		"private_key",
	}

	// businessDomainKeywords defines business configuration domains strictly outside V1 instance admin scope.
	businessDomainKeywords = []string{
		"tax",
		"taxes",
		"account",
		"accounts",
		"accounting",
		"company",
		"companies",
		"product",
		"products",
		"customer",
		"customers",
		"partner",
		"partners",
		"sale",
		"sales",
		"purchase",
		"purchases",
		"crm",
		"inventory",
		"stock",
		"warehouse",
		"warehouses",
		"project",
		"projects",
		"hr",
		"payroll",
		"employee",
		"employees",
		"pos",
		"ecommerce",
		"website_sale",
		"appointment",
		"calendar_booking",
	}
)

// scanForCredentials recursively traverses parsed JSON structures to detect credential-like keys
// at any depth. To prevent secret disclosure, it never echoes values.
func scanForCredentials(v interface{}) error {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, child := range val {
			norm := strings.ToLower(strings.TrimSpace(k))
			for _, kw := range credentialKeywords {
				if strings.Contains(norm, kw) {
					return clicore.UsageError("CREDENTIAL_IN_MANIFEST_FORBIDDEN",
						"manifest contains a credential or secret field; credentials must never enter manifests and must remain in local profiles and keyring").
						WithRemediation("Remove credentials from the manifest. Configure credentials in local profiles using 'odoo credential set'.")
				}
			}
			if err := scanForCredentials(child); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, item := range val {
			if err := scanForCredentials(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// ParseAndValidateAdminManifest parses raw JSON data and applies strict validation rules.
// It fails closed before any provider contact on syntax errors, unsupported domains, credential leaks,
// forbidden module declarations, non-canonical casing, null/wrong types, or invalid schema shapes.
func ParseAndValidateAdminManifest(data []byte) (*AdminManifest, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, clicore.UsageError("INVALID_JSON", "manifest data is empty").
			WithRemediation("Provide a non-empty JSON manifest document.")
	}

	// First pass: parse as generic interface and recursively check for credential leakage at any depth.
	var genericDoc interface{}
	if err := json.Unmarshal(data, &genericDoc); err != nil {
		return nil, clicore.UsageError("INVALID_JSON", "manifest is not valid JSON: %s", err.Error()).
			WithRemediation("Ensure the file contains valid JSON formatted according to the odoo.admin.config/v1 schema.")
	}
	if err := scanForCredentials(genericDoc); err != nil {
		return nil, err
	}

	// Verify root is a JSON object (not null, array, string, number, or boolean).
	rootMap, ok := genericDoc.(map[string]interface{})
	if !ok || rootMap == nil {
		return nil, clicore.UsageError("INVALID_JSON", "manifest root must be a non-null JSON object").
			WithRemediation("Format the manifest as a top-level JSON object: {\"schemaVersion\": ...}")
	}

	// Second pass: inspect top-level fields for exact canonical casing, unsupported domains, and unknown keys.
	for rawKey := range rootMap {
		normLower := strings.ToLower(strings.TrimSpace(rawKey))

		// Check for module configuration attempts.
		if normLower == "modules" || normLower == "module" {
			return nil, clicore.UsageError("MODULE_INSTALL_UNSUPPORTED_STANDALONE",
				"module installation is not supported in standalone 'odoo admin config'; module installation is governed exclusively through ASAO capability packs ('asao odoo pack plan/apply')").
				WithRemediation("Use governed 'asao odoo pack plan <pack>' and 'asao odoo pack apply <pack>' to manage Odoo modules.")
		}

		// Check exact canonical casing.
		switch rawKey {
		case "schemaVersion", "metadata", "users":
			// Canonical exact field names allowed in V1.
		default:
			// Check if this is an attempted business configuration domain.
			for _, domain := range businessDomainKeywords {
				if normLower == domain || strings.HasPrefix(normLower, domain+"_") {
					return nil, clicore.UsageError("UNSUPPORTED_CONFIGURATION_DOMAIN",
						"business configuration is out of scope; V1 admin config is strictly scoped to existing user language preferences").
						WithRemediation("Remove business configuration fields (tax, accounting, company, products, etc.). V1 only supports user language preferences under 'users'.")
				}
			}
			return nil, clicore.UsageError("UNKNOWN_MANIFEST_FIELD",
				"unknown field %q in manifest; top-level fields must use exact canonical names ('schemaVersion', 'metadata', 'users')", rawKey).
				WithRemediation("Supported top-level fields in V1 are exact: 'schemaVersion', 'metadata', 'users'. Differently-cased aliases are rejected.")
		}
	}

	// Validate schemaVersion strictly.
	rawVersion, hasVersion := rootMap["schemaVersion"]
	if !hasVersion {
		return nil, clicore.UsageError("MISSING_SCHEMA_VERSION", "manifest must specify 'schemaVersion': %q", AdminConfigSchemaVersion).
			WithRemediation("Set 'schemaVersion': '" + AdminConfigSchemaVersion + "' at the top level of the manifest.")
	}
	versionStr, isStr := rawVersion.(string)
	if !isStr {
		return nil, clicore.UsageError("INVALID_SCHEMA_VERSION", "field 'schemaVersion' must be a JSON string").
			WithRemediation("Set 'schemaVersion' as a JSON string: '" + AdminConfigSchemaVersion + "'.")
	}
	if strings.TrimSpace(versionStr) != AdminConfigSchemaVersion {
		return nil, clicore.UsageError("UNSUPPORTED_SCHEMA_VERSION",
			"unsupported schemaVersion; expected exact %q", AdminConfigSchemaVersion).
			WithRemediation("Set 'schemaVersion': '" + AdminConfigSchemaVersion + "'.")
	}

	manifest := AdminManifest{
		SchemaVersion: AdminConfigSchemaVersion,
	}

	// Validate metadata strictly if present.
	if rawMeta, hasMeta := rootMap["metadata"]; hasMeta {
		if rawMeta == nil {
			return nil, clicore.UsageError("INVALID_METADATA", "field 'metadata' cannot be null; omit the field or supply a JSON object").
				WithRemediation("Provide a JSON object for metadata or omit the field entirely.")
		}
		metaMap, ok := rawMeta.(map[string]interface{})
		if !ok {
			return nil, clicore.UsageError("INVALID_METADATA", "field 'metadata' must be a JSON object").
				WithRemediation("Specify metadata as an object: {\"name\": \"...\", \"description\": \"...\"}")
		}
		for metaKey, metaVal := range metaMap {
			switch metaKey {
			case "name":
				if metaVal == nil {
					return nil, clicore.UsageError("INVALID_METADATA", "metadata field 'name' cannot be null; omit the field or supply a string").
						WithRemediation("Specify 'name' as a string or omit the field from metadata.")
				}
				nameStr, ok := metaVal.(string)
				if !ok {
					return nil, clicore.UsageError("INVALID_METADATA", "metadata field 'name' must be a string").
						WithRemediation("Specify 'name' as a string.")
				}
				manifest.Metadata.Name = strings.TrimSpace(nameStr)
			case "description":
				if metaVal == nil {
					return nil, clicore.UsageError("INVALID_METADATA", "metadata field 'description' cannot be null; omit the field or supply a string").
						WithRemediation("Specify 'description' as a string or omit the field from metadata.")
				}
				descStr, ok := metaVal.(string)
				if !ok {
					return nil, clicore.UsageError("INVALID_METADATA", "metadata field 'description' must be a string").
						WithRemediation("Specify 'description' as a string.")
				}
				manifest.Metadata.Description = strings.TrimSpace(descStr)
			default:
				return nil, clicore.UsageError("UNSUPPORTED_METADATA_FIELD",
					"metadata contains unsupported field %q; only exact fields 'name' and 'description' are permitted", metaKey).
					WithRemediation("Remove unsupported fields from metadata.")
			}
		}
	}

	// Validate users strictly.
	rawUsers, hasUsers := rootMap["users"]
	if !hasUsers {
		return nil, clicore.UsageError("MISSING_USERS_FIELD", "manifest must specify 'users'").
			WithRemediation("Declare at least one user preference under 'users'.")
	}
	if rawUsers == nil {
		return nil, clicore.UsageError("INVALID_USER_SPECIFICATION", "field 'users' cannot be null; must be a JSON array of user objects").
			WithRemediation("Specify 'users' as an array of user objects: [{\"login\": \"...\", \"lang\": \"...\"}]")
	}
	userSlice, ok := rawUsers.([]interface{})
	if !ok {
		return nil, clicore.UsageError("INVALID_USER_SPECIFICATION", "field 'users' must be a JSON array of user objects").
			WithRemediation("Specify 'users' as an array of user objects: [{\"login\": \"...\", \"lang\": \"...\"}]")
	}
	if len(userSlice) == 0 {
		return nil, clicore.UsageError("EMPTY_MANIFEST", "manifest must declare at least one user language preference under 'users'").
			WithRemediation("Declare at least one user preference under 'users'.")
	}

	seenUsers := make(map[string]bool, len(userSlice))
	normalizedUsers := make([]UserPreference, 0, len(userSlice))
	for _, rawEntry := range userSlice {
		if rawEntry == nil {
			return nil, clicore.UsageError("INVALID_USER_SPECIFICATION", "user entry cannot be null").
				WithRemediation("Each user entry in 'users' must be a non-null JSON object.")
		}
		userMap, ok := rawEntry.(map[string]interface{})
		if !ok {
			return nil, clicore.UsageError("INVALID_USER_SPECIFICATION", "each user entry must be a JSON object").
				WithRemediation("Each user entry in 'users' must be a JSON object: {\"login\": \"...\", \"lang\": \"...\"}")
		}

		// Enforce exact canonical field names in user objects.
		for userKey := range userMap {
			switch userKey {
			case "login", "lang":
				// Allowed exact canonical fields.
			default:
				return nil, clicore.UsageError("UNSUPPORTED_USER_FIELD",
					"user entry contains unsupported field %q; only exact fields 'login' and 'lang' are permitted", userKey).
					WithRemediation("User entries may only specify exact fields 'login' and 'lang'. V1 does not create users or manage passwords.")
			}
		}

		rawLogin, hasLogin := userMap["login"]
		if !hasLogin || rawLogin == nil {
			return nil, clicore.UsageError("MISSING_USER_FIELD", "user entry is missing required field 'login'").
				WithRemediation("Specify 'login' as a non-empty string in each user entry.")
		}
		loginStr, ok := rawLogin.(string)
		if !ok {
			return nil, clicore.UsageError("INVALID_USER_LOGIN", "field 'login' must be a string").
				WithRemediation("Specify 'login' as a string.")
		}
		trimmedLogin := strings.TrimSpace(loginStr)
		if trimmedLogin == "" {
			return nil, clicore.UsageError("INVALID_USER_LOGIN", "user login cannot be blank").
				WithRemediation("Provide a valid non-empty login string for each user preference.")
		}
		if seenUsers[trimmedLogin] {
			return nil, clicore.UsageError("DUPLICATE_USER", "duplicate user login in manifest").
				WithRemediation("Each user login must appear at most once in the 'users' list.")
		}

		rawLang, hasLang := userMap["lang"]
		if !hasLang || rawLang == nil {
			return nil, clicore.UsageError("MISSING_USER_FIELD", "user entry is missing required field 'lang'").
				WithRemediation("Specify 'lang' as a non-empty language code in each user entry.")
		}
		langStr, ok := rawLang.(string)
		if !ok {
			return nil, clicore.UsageError("INVALID_USER_LANG", "field 'lang' must be a string").
				WithRemediation("Specify 'lang' as a string (e.g. 'en_US', 'vi_VN').")
		}
		trimmedLang := strings.TrimSpace(langStr)
		if trimmedLang == "" {
			return nil, clicore.UsageError("INVALID_USER_LANG", "user preference must declare a non-empty lang code").
				WithRemediation("Specify an active Odoo language code (e.g. en_US, vi_VN).")
		}
		if !langCodePattern.MatchString(trimmedLang) {
			return nil, clicore.UsageError("INVALID_USER_LANG",
				"invalid lang format (expected format like en_US or vi_VN)").
				WithRemediation("Specify language codes in standard format (e.g. en_US, vi_VN, fr_FR).")
		}

		seenUsers[trimmedLogin] = true
		normalizedUsers = append(normalizedUsers, UserPreference{
			Login: trimmedLogin,
			Lang:  trimmedLang,
		})
	}
	manifest.Users = normalizedUsers

	return &manifest, nil
}
