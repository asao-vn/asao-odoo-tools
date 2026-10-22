package odoo

import "testing"

// A caller that keeps its own id for a contact (ASAO's services gate writes asao-student:<id>) finds it again by that
// reference, not by a name or an email that two people can share. Odoo's field is ref; the catalog has always let a
// create write it, and the caller vocabulary now names it.
func TestContactCreateTakesAReference(t *testing.T) {
	reference, ok := Odoo19Contracts["identity.contact.create"].InputByProviderField("ref")
	if !ok || reference.Name != "reference" || reference.Type != "string" {
		t.Fatalf("identity.contact.create must accept ref as the semantic string reference: %+v", reference)
	}
	allowed := false
	for _, field := range CatalogByID["identity.contact.create"].AllowedFields {
		allowed = allowed || field == "ref"
	}
	if !allowed {
		t.Fatal("identity.contact.create must be allowed to write ref")
	}
}
