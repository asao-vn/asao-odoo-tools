package review

import (
	"strings"
	"testing"
)

// Every built-in declared check passes its own cases: the gate a check file goes through before it runs.
func TestEveryDeclaredCheckPassesItsOwnCases(t *testing.T) {
	files := BuiltinCheckFiles()
	if len(files) < 2 {
		t.Fatalf("built-in check files: %d", len(files))
	}
	for _, file := range files {
		if err := file.TestCases(); err != nil {
			t.Error(err)
		}
		if _, registered := checks[file.ID]; !registered {
			t.Errorf("%s is not registered", file.ID)
		}
	}
}

// A file the engine cannot trust is refused before anything runs.
func TestACheckFileIsRefusedWhenItCannotBeTrusted(t *testing.T) {
	valid := string(mustRead(t, "checks/O23.json"))
	cases := map[string]struct{ from, to, want string }{
		"another schema":       {`"odoo-cli.check/v1"`, `"odoo-cli.check/v9"`, "schema"},
		"an unknown key":       {`"version": 1,`, `"version": 1, "code": "x",`, "unknown field"},
		"a write operation":    {`"people.expense.list"`, `"people.expense.create"`, "not a read operation"},
		"an unknown operation": {`"people.expense.list"`, `"people.expense.lsit"`, "not a read operation"},
		"a missing parameter":  {`"within": "window_days"`, `"within": "days"`, "params does not hold"},
		"an unknown matcher":   {`"kind": "pairs"`, `"kind": "magic"`, "not pairs or groups"},
		"a severity unknown":   {`"then": "NEN_XU_LY"`, `"then": "LOW"`, "is not XEM_NGAY"},
		"a condition as code":  {`"if": "gap == 0"`, `"if": "os.exit(1)"`, "not 'name op number'"},
		"no look-alike case":   {`"expect": []`, `"expect": [{"evidence": [5, 6]}]`, "one it must leave alone"},
	}
	for name, c := range cases {
		changed := strings.Replace(valid, c.from, c.to, 1)
		if changed == valid {
			t.Fatalf("%s: the replacement did not apply", name)
		}
		if _, err := ParseCheck([]byte(changed)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// A person's own check runs beside the built-in ones once its cases pass; one whose cases fail, or that
// takes a built-in id, is refused.
func TestAPersonsCheckIsAddedOnlyWhenItsCasesPass(t *testing.T) {
	own := strings.NewReplacer(`"id": "O23"`, `"id": "X-CLAIMS-TWICE"`).Replace(string(mustRead(t, "checks/O23.json")))
	file, err := ParseCheck([]byte(own))
	if err != nil {
		t.Fatal(err)
	}
	defer delete(checks, "X-CLAIMS-TWICE")
	if err := RegisterCheck(file); err != nil {
		t.Fatal(err)
	}
	if _, ok := checks["X-CLAIMS-TWICE"]; !ok {
		t.Fatal("not registered")
	}
	broken := strings.Replace(own, `"id": "X-CLAIMS-TWICE"`, `"id": "X-BROKEN"`, 1)
	broken = strings.Replace(broken, `"severity": "XEM_NGAY"`, `"severity": "THEO_DOI"`, 1)
	file, _ = ParseCheck([]byte(broken))
	if err := RegisterCheck(file); err == nil || !strings.Contains(err.Error(), "expected THEO_DOI") {
		t.Fatalf("a check whose cases fail: %v", err)
	}
	clash := strings.Replace(own, `"id": "X-CLAIMS-TWICE"`, `"id": "O01"`, 1)
	file, _ = ParseCheck([]byte(clash))
	if err := RegisterCheck(file); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("a built-in id: %v", err)
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	data, err := builtinCheckFiles.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
