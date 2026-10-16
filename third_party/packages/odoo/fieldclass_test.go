package odoo

import (
	"strings"
	"testing"
)

func TestClassifyFieldByNameAndType(t *testing.T) {
	cases := []struct {
		name, odooType string
		want           FieldClass
	}{
		{"name", "char", FieldPlain},
		{"amount_total", "monetary", FieldPlain},
		{"partner_id", "many2one", FieldPlain},
		{"state", "selection", FieldPlain},
		{"email", "char", FieldPersonal},
		{"email_from", "char", FieldPersonal},
		{"mobile", "char", FieldPersonal},
		{"street2", "char", FieldPersonal},
		{"vat", "char", FieldPersonal},
		{"x_phone_2", "char", FieldPersonal},
		{"acc_number", "char", FieldPersonal},
		{"private_street", "char", FieldPersonal},
		{"description", "", FieldFreeText},
		{"x_customer_remark", "char", FieldFreeText},
		{"comment", "html", FieldFreeText},
		{"x_anything", "text", FieldFreeText},
		{"image_1920", "binary", FieldFile},
		{"api_key", "char", FieldCredential},
		{"password", "", FieldCredential},
	}
	for _, c := range cases {
		if got := ClassifyField(c.name, c.odooType); got != c.want {
			t.Errorf("%s (%s): %s, want %s", c.name, c.odooType, got, c.want)
		}
	}
}

func TestLooksLikeFreeText(t *testing.T) {
	for value, want := range map[string]bool{
		"SO/2026/0042":             false,
		"Công ty TNHH Phú Mỹ":      false,
		"line one\nline two":       true,
		strings.Repeat("chữ ", 50): true,
		strings.Repeat("a", 160):   false,
		strings.Repeat("a", 161):   true,
	} {
		if got := LooksLikeFreeText(value); got != want {
			t.Errorf("%q: %v, want %v", value[:min(len(value), 30)], got, want)
		}
	}
}
