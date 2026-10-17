package standalone

import (
	"strings"
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
	// A write whose own input is named like a read flag takes it as that input: --order is the order a
	// quotation line belongs to, and the call goes on to look for its profile.
	if _, envelope := runRoutinesCommand(t, "execute", "--operation", "sales.quotation.line.add", "--set", "order=7", "--set", "product=9", "--order", "7"); errorCode(envelope) == "AERP_READ_FLAG_ON_WRITE" {
		t.Fatalf("an input named order was refused as a read flag: %v", envelope["error"])
	}
	// The generated command, as an agent calls it: past the guard, it goes on to look for its profile.
	_, out, _ := runTrace(t, "sales", "quotation", "line", "add", "--order", "7", "--product", "9", "--quantity", "10", "--output", "json")
	if strings.Contains(string(out), "AERP_READ_FLAG_ON_WRITE") || !strings.Contains(string(out), "PROFILE_FILE_NOT_FOUND") {
		t.Fatalf("sales quotation line add --order: %s", out)
	}
	// A read takes them, and goes on to look for its profile.
	if _, envelope := runRoutinesCommand(t, "execute", "--operation", "sales.quotation.list", "--limit", "5"); errorCode(envelope) == "AERP_READ_FLAG_ON_WRITE" {
		t.Fatalf("a read was refused its limit: %v", envelope["error"])
	}
}
