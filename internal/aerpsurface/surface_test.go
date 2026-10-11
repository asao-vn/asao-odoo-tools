package aerpsurface

import (
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func TestBuildRequest_RefusesAdminModuleInstall(t *testing.T) {
	surface := Surface{
		CatalogListCommand:     "asao aerp catalog list",
		CatalogDescribeCommand: "asao aerp catalog describe",
	}
	inv := &clicore.Invocation{
		Raw: map[string][]string{
			"operation": {"admin.module.install"},
		},
	}
	_, err := surface.BuildRequest(inv)
	if err == nil {
		t.Fatal("expected error for admin.module.install, got nil")
	}
	cliErr, ok := err.(*clicore.Error)
	if !ok {
		t.Fatalf("expected *clicore.Error, got %T: %v", err, err)
	}
	if cliErr.Code != "AERP_OPERATION_REFUSED" {
		t.Errorf("Code = %q, want %q", cliErr.Code, "AERP_OPERATION_REFUSED")
	}
	if cliErr.Category != clicore.CategoryUsage {
		t.Errorf("Category = %q, want %q", cliErr.Category, clicore.CategoryUsage)
	}
	wantMsg := "admin.module.install is executed only through a capability pack"
	if cliErr.Message != wantMsg {
		t.Errorf("Message = %q, want %q", cliErr.Message, wantMsg)
	}
	wantRemediation := "Run: asao odoo pack plan public-business, then asao odoo pack apply public-business --apply --confirm-impact"
	if cliErr.Remediation != wantRemediation {
		t.Errorf("Remediation = %q, want %q", cliErr.Remediation, wantRemediation)
	}
}
