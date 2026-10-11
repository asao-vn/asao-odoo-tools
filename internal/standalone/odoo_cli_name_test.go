package standalone_test

import (
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
)

// The public Odoo client is named odoo-cli: Odoo's own server installs an `odoo` executable. Under
// that name, and under its former name, it carries the Odoo flavour (the raw call); the historical
// aerp tree does not.
func TestOdooCLICarriesTheOdooFlavour(t *testing.T) {
	children := func(program string) map[string]bool {
		names := map[string]bool{}
		for _, child := range standalone.RootFor(program).Children {
			names[child.Name] = true
		}
		return names
	}
	for _, program := range []string{"odoo-cli", "odoo"} {
		if names := children(program); !names["call"] {
			t.Fatalf("%s lacks the Odoo flavour: %v", program, names)
		}
	}
	if names := children("aerp"); names["call"] {
		t.Fatalf("aerp gained the Odoo flavour: %v", names)
	}
}
