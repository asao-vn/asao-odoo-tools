package odoo

import (
	"context"
	"testing"
	"time"
)

// A fresh database runs its first scheduled actions right after its first module installs, and Odoo
// refuses every module operation while one runs, before it changes anything. The install is called
// again while the module is still uninstalled; any other outcome is reported at once.
func TestModuleInstallIsCalledAgainWhileAScheduledActionHoldsOdoo(t *testing.T) {
	busy := &OdooError{Code: 422, Message: "odoo.exceptions.UserError", Detail: "Odoo hiện đang xử lý một tác vụ đã lên lịch."}
	denied := &OdooError{Code: 403, Message: "odoo.exceptions.AccessError", Detail: "Access Denied"}
	cases := []struct {
		name      string
		refusal   *OdooError
		refusals  int
		marked    string // the module's state after a refusal
		wantCalls int
		wantOK    bool
	}{
		{"refused twice, then installed", busy, 2, "uninstalled", 3, true},
		{"refused every time", busy, 99, "uninstalled", 3, false},
		{"refused once the module was marked", busy, 1, "to install", 1, false},
		{"refused for access", denied, 1, "uninstalled", 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			tr := &fakeTransport{
				callMethod: func(string, string) (interface{}, error) {
					calls++
					if calls <= c.refusals {
						return nil, c.refusal
					}
					return true, nil
				},
				read: func(string, []int64) ([]map[string]interface{}, error) {
					state := "uninstalled"
					switch {
					case calls > c.refusals:
						state = "installed"
					case calls > 0:
						state = c.marked
					}
					return []map[string]interface{}{{"id": 630.0, "name": "website", "state": state}}, nil
				},
			}
			adapter := NewAdapter(tr)
			adapter.ModuleBusyWaits = []time.Duration{time.Millisecond, time.Millisecond}
			_, err := adapter.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "admin.module.install", Input: `{"target_id":630}`})
			if (err == nil) != c.wantOK || calls != c.wantCalls {
				t.Fatalf("err %v after %d calls: want ok=%v after %d", err, calls, c.wantOK, c.wantCalls)
			}
		})
	}
}
