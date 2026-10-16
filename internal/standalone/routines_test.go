package standalone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/routines"
)

func runRoutinesCommand(t *testing.T, args ...string) (int, map[string]interface{}) {
	t.Helper()
	var out, diag bytes.Buffer
	code := clicore.Run(context.Background(), RootFor("odoo-cli"), append(args, "--output", "json"), clicore.Streams{Out: &out, Diagnostics: &diag})
	var envelope map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("%v: stdout is not one JSON document: %v\n%s", args, err, out.String())
	}
	return code, envelope
}

func errorCode(envelope map[string]interface{}) string {
	failure, _ := envelope["error"].(map[string]interface{})
	code, _ := failure["code"].(string)
	return code
}

// init writes the starter routines for a profile; list and status read the file back without
// contacting Odoo; a file is never replaced unless the person says so.
func TestRoutinesInitListAndStatusContactNothing(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "routines.toml")
	output := filepath.Join(folder, "out")
	code, envelope := runRoutinesCommand(t, "routines", "init", "--file", path, "--profile", "lab", "--agent", "claude", "--output-folder", output)
	if code != clicore.ExitOK {
		t.Fatalf("init: %d %v", code, envelope)
	}
	if code, envelope = runRoutinesCommand(t, "routines", "init", "--file", path, "--profile", "lab"); errorCode(envelope) != "ROUTINES_FILE_EXISTS" {
		t.Fatalf("init over a file: %d %v", code, envelope)
	}
	code, envelope = runRoutinesCommand(t, "routines", "list", "--file", path)
	data, _ := envelope["data"].(map[string]interface{})
	if code != clicore.ExitOK || data["count"] != float64(9) || data["profile"] != "lab" || data["agent"] != "claude" {
		t.Fatalf("list: %d %v", code, envelope)
	}
	code, envelope = runRoutinesCommand(t, "routines", "status", "--file", path)
	data, _ = envelope["data"].(map[string]interface{})
	if code != clicore.ExitOK || !strings.Contains(data["index"].(string), "| Công nợ quá hạn (`cong-no-qua-han`) | finance | hằng ngày 07:30 | - |") {
		t.Fatalf("status: %d %v", code, envelope)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("status wrote the output folder: %v", err)
	}
	other := filepath.Join(folder, "other.toml")
	for _, c := range []struct {
		extra []string
		want  string
	}{
		{[]string{"--template", "nope"}, "UNKNOWN_TEMPLATE"},
		{[]string{"--agent", "claude -p"}, "INVALID_FLAG_VALUE"},
		{[]string{"--file", filepath.Join(folder, "r.yaml")}, "INVALID_FLAG_VALUE"},
	} {
		argv := append([]string{"routines", "init", "--profile", "lab", "--force", "--file", other}, c.extra...)
		if c.extra[0] == "--file" {
			argv = append([]string{"routines", "init", "--profile", "lab", "--force"}, c.extra...)
		}
		if _, envelope := runRoutinesCommand(t, argv...); errorCode(envelope) != c.want {
			t.Errorf("init %v: %v, want %s", c.extra, envelope["error"], c.want)
		}
	}
	jsonPath := filepath.Join(folder, "routines.json")
	if code, envelope := runRoutinesCommand(t, "routines", "init", "--file", jsonPath, "--profile", "lab", "--template", "len-viec-dinh-ky",
		"--agent", `["my-agent", "--read", "{prompt}"]`); code != clicore.ExitOK {
		t.Fatalf("init json: %v", envelope)
	}
	if code, envelope := runRoutinesCommand(t, "routines", "list", "--file", jsonPath); code != clicore.ExitOK {
		t.Fatalf("list json: %v", envelope)
	}
}

// A file the asao-erp SDK wrote, and the same routines in YAML or JSON, read alike.
func TestRoutinesFilesFromTheSDKAndInYAML(t *testing.T) {
	folder := t.TempDir()
	files := map[string]string{
		"sdk.toml": `# Việc định kỳ trên Odoo: asao-erp routines --help
profile = "cong-ty"
output = "~/asao-routines"
agent = ["codex", "exec", "-"]
language = "vi"

[[routine]]
id = "len-viec-dinh-ky"
title = "Lên việc lặp lại trong Odoo"
area = "work"
every = "monthly"
at = "06:30"
on = "last"
writes = true
agent = "none"

  [[routine.step]]
  name = "khoa-so"
  ensure_task = { name = "Khoá sổ tháng", project = "Vận hành", recurring = true, repeat_every = 1, repeat_unit = "month", due_on = "{next_month_start+4}" }
  note = "Việc lặp hằng tháng"
`,
		"same.yaml": `profile: cong-ty
routine:
  - id: len-viec-dinh-ky
    every: monthly
    on: last
    at: "06:30"
    writes: true
    step:
      - name: khoa-so
        ensure_task: {name: Khoá sổ tháng, project: Vận hành, due_on: "{next_month_start+4}"}
`,
		"same.json": `{"profile": "cong-ty", "routines": [{"id": "len-viec-dinh-ky", "every": "monthly", "on": "last", "at": "06:30", "writes": true,
 "steps": [{"name": "khoa-so", "ensure_task": {"name": "Khoá sổ tháng", "project": "Vận hành", "due_on": "{next_month_start+4}"}}]}]}`,
	}
	for name, content := range files {
		path := filepath.Join(folder, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeRoutines(path, []byte(content))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		file, err := routines.FromMap(decoded)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		routine := file.Routines[0]
		if routine.On != "last" || !routine.Writes || routine.Steps[0].Kind != "ensure_task" || routine.Steps[0].Values["project"] != "Vận hành" {
			t.Errorf("%s: %+v", name, routine)
		}
	}
}

// An agent may list the starter routines and read a file's routines and their latest runs, from the
// file the person started the server with. Running routines, writing a file and installing a timer
// stay with the person.
func TestRoutinesAreReadByAgentsAndRunByThePerson(t *testing.T) {
	tools, err := agentsurface.ProjectAs(RootFor("odoo-cli"), "odoo-cli", agentsurface.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	offered := map[string]agentsurface.Tool{}
	for _, tool := range tools {
		offered[tool.Name] = tool
	}
	for _, name := range []string{"routines_templates", "routines_list", "routines_status"} {
		tool, ok := offered[name]
		if !ok || tool.AgentAccess != clicore.AgentAccessRead {
			t.Fatalf("%s is not offered as a read (%v)", name, tool.AgentAccess)
		}
		if _, given := tool.InputSchema["properties"].(map[string]interface{})["file"]; given {
			t.Errorf("%s lets an agent name the file", name)
		}
	}
	for _, name := range []string{"routines_run", "routines_due", "routines_init", "routines_install", "routines_uninstall"} {
		if _, ok := offered[name]; ok {
			t.Errorf("%s is offered to an agent", name)
		}
	}
}

// A read or group step reads; a routine changes Odoo only through its write steps, never by
// installing a module.
func TestRoutineStepsKeepToTheirKind(t *testing.T) {
	odoo := &routineOdoo{config: newProgramConfig("odoo-cli")}
	refusal := func(err error) string {
		var refused *routines.Refusal
		if !errors.As(err, &refused) {
			return "not a refusal: " + err.Error()
		}
		return refused.Code
	}
	if err := odoo.readOnly("finance.invoice.list"); err != nil {
		t.Fatalf("a read operation: %v", err)
	}
	if got := refusal(odoo.readOnly("sales.order.confirm")); got != "ROUTINE_STEP_WRITES" {
		t.Errorf("a write named by a read step: %s", got)
	}
	if got := refusal(odoo.readOnly("no.such.list")); got == "ROUTINE_STEP_WRITES" || strings.HasPrefix(got, "not a refusal") {
		t.Errorf("an unknown operation: %s", got)
	}
	if _, err := odoo.Execute(context.Background(), "work.task.create", nil, nil); refusal(err) != "ROUTINE_READS_ONLY" {
		t.Errorf("a write in a routine that reads only: %v", err)
	}
	odoo.writes = true
	if _, err := odoo.Execute(context.Background(), "admin.module.install", nil, nil); refusal(err) != "AERP_OPERATION_REFUSED" {
		t.Errorf("a module install: %v", err)
	}
	if meanings := odoo.Describe("finance.invoice.list"); len(meanings) == 0 || meanings[0].Name == "" {
		t.Errorf("describe: %v", meanings)
	}
}

// Each scheduler reads its own quoting: a path with a space, a percent sign, a dollar sign or a quote
// reaches the command whole.
func TestTimerFilesQuoteForTheirSchedulers(t *testing.T) {
	command := []string{"/opt/odoo cli/odoo-cli", "routines", "due", "--file", "/home/a/100%/$HOME/it's.toml"}
	environment := [][2]string{{"PATH", "/usr/bin:/home/a/bin"}, {"AERP_CREDENTIAL_FILE", "/home/a/cred 1.json"}}
	service, timer := systemdUnits(command, environment, "/home/a/out/scheduler.log", 10)
	for _, want := range []string{
		`ExecStart="/opt/odoo cli/odoo-cli" "routines" "due" "--file" "/home/a/100%%/$$HOME/it's.toml"`,
		`Environment="AERP_CREDENTIAL_FILE=/home/a/cred 1.json"`,
	} {
		if !strings.Contains(service, want+"\n") {
			t.Errorf("service lacks %s:\n%s", want, service)
		}
	}
	if !strings.Contains(timer, "OnCalendar=*:0/10\n") || !strings.Contains(timer, "Persistent=true") {
		t.Errorf("timer:\n%s", timer)
	}
	if line := cronLine(command, environment, "/home/a/log", 15); !strings.Contains(line, `'/home/a/100%/$HOME/it'"'"'s.toml'`) ||
		!strings.HasPrefix(line, "*/15 * * * * PATH=/usr/bin:/home/a/bin AERP_CREDENTIAL_FILE='/home/a/cred 1.json' '/opt/odoo cli/odoo-cli' routines") ||
		!strings.HasSuffix(line, "# odoo-cli-routines") {
		t.Errorf("cron: %s", line)
	}
	if plist := launchdPlist([]string{"/a&b/odoo-cli"}, nil, "/l", 5); !strings.Contains(plist, "<string>/a&amp;b/odoo-cli</string>") || !strings.Contains(plist, "<integer>300</integer>") {
		t.Errorf("plist:\n%s", plist)
	}
	if got := windowsCommandLine([]string{`C:\Program Files\odoo-cli.exe`, "routines", "due"}); got != `"C:\Program Files\odoo-cli.exe" routines due` {
		t.Errorf("windows: %s", got)
	}
}
