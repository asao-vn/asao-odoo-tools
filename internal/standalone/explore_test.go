package standalone

import (
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func TestExploreNeverReachesASecret(t *testing.T) {
	for _, model := range []string{"ir.config_parameter", "ir.mail_server", "payment.provider", "res.users.apikeys", "certificate.certificate", "account.online.link"} {
		if !exploreSecretModel(model) {
			t.Errorf("%s is explorable", model)
		}
	}
	for _, model := range []string{"res.partner", "planning.slot", "appointment.type", "x_custom_record"} {
		if exploreSecretModel(model) {
			t.Errorf("%s is refused", model)
		}
	}
	for _, field := range []string{"password", "signup_token", "client_secret", "api_key", "l10n_vn_edi_password", "totp_secret"} {
		if !exploreSecretField(field) {
			t.Errorf("%s is readable", field)
		}
	}

	// A filter on a secret field, even nested under an operator or through a relation, is refused.
	invocation := &clicore.Invocation{Raw: map[string][]string{"filter": {`["|",["name","=","a"],["user_ids.password","!=",false]]`}}}
	if _, err := exploreDomain(invocation); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("a filter through a relation's password was accepted: %v", err)
	}

	// The default projection leaves out secrets, binaries and relations' lists; asked fields must be known.
	described := map[string]map[string]interface{}{
		"name":         {"type": "char", "store": true},
		"display_name": {"type": "char", "store": false},
		"line_ids":     {"type": "one2many", "store": true},
		"amount_total": {"type": "monetary", "store": true},
	}
	projected, err := exploreProjection(&clicore.Invocation{Raw: map[string][]string{}}, described)
	if err != nil || strings.Join(projected, ",") != "id,display_name,amount_total,name" {
		t.Fatalf("projection = %v, %v", projected, err)
	}
	if _, err := exploreProjection(&clicore.Invocation{Raw: map[string][]string{"fields": {"password"}}}, described); err == nil {
		t.Fatal("an unknown or secret field was projected")
	}

	// A secret that reached an answer anyway (a computed field, an unexpected model) is removed.
	rows := records([]interface{}{map[string]interface{}{"id": 1.0, "name": "a", "access_token": "x"}})
	if _, leaked := rows[0]["access_token"]; leaked {
		t.Fatalf("a token reached the answer: %v", rows)
	}
}

func TestExploreIsOfferedToAnAgentAsReadsOnTheServersProfile(t *testing.T) {
	tools, err := agentsurface.ProjectAs(RootFor("odoo-cli"), "odoo-cli", agentsurface.Filter{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]agentsurface.Tool{}
	for _, tool := range tools {
		found[tool.Name] = tool
	}
	for _, name := range []string{"explore_models", "explore_fields", "explore_search", "explore_group"} {
		tool, ok := found[name]
		if !ok {
			t.Fatalf("%s is not offered", name)
		}
		if _, offered := tool.InputSchema["properties"].(map[string]interface{})["profile"]; offered {
			t.Errorf("%s lets the agent choose the profile", name)
		}
	}
}
