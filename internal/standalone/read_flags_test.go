package standalone

import (
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// The flags that shape a read are refused on an operation that changes Odoo, before any profile is
// read or Odoo contacted: a caller that pages through what it takes for a read never runs a write.
func TestReadFlagsAreRefusedOnAWrite(t *testing.T) {
	t.Setenv("AERP_PROFILE_FILE", t.TempDir()+"/none.json")
	for _, flags := range [][]string{
		{"--limit", "80"}, {"--filter", `[["id","=",1]]`}, {"--order", "id"}, {"--offset", "5"},
		{"--group-by", "state"}, {"--aggregate", "amount_total:sum"},
	} {
		args := append([]string{"execute", "--operation", "sales.order.confirm", "--provider-target-id", "42"}, flags...)
		code, envelope := runRoutinesCommand(t, args...)
		if code != clicore.ExitUsage || errorCode(envelope) != "AERP_READ_FLAG_ON_WRITE" {
			t.Errorf("%v: %d %v", flags, code, envelope["error"])
		}
	}
	// A read takes them, and goes on to look for its profile.
	if _, envelope := runRoutinesCommand(t, "execute", "--operation", "sales.quotation.list", "--limit", "5"); errorCode(envelope) == "AERP_READ_FLAG_ON_WRITE" {
		t.Fatalf("a read was refused its limit: %v", envelope["error"])
	}
}
