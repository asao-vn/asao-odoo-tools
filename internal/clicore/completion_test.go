package clicore_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func buildSampleTree() *clicore.Command {
	sub1 := &clicore.Command{
		Name:    "employee",
		Summary: "Manage employees with quotes ' and colons: and $dollars [brackets]`ticks`",
		Flags: []clicore.Flag{
			{Name: "role", Kind: clicore.KindString, Description: "Role: primary/secondary [internal] 'quoted' $money `exec`", Enum: []string{"dev", "qa", "pm"}},
			{Name: "active", Kind: clicore.KindBool, Description: "Whether active"},
		},
	}
	people := &clicore.Command{
		Name:     "people",
		Summary:  "People & HR domain",
		Children: []*clicore.Command{sub1},
	}
	root := &clicore.Command{
		Name:     "demo",
		Program:  "demo",
		Summary:  "Demo application",
		Children: []*clicore.Command{people},
		Flags: []clicore.Flag{
			{Name: "config", Kind: clicore.KindString, Description: "Path to config file"},
		},
	}
	clicore.Prepare(root)
	return root
}

func TestCompletion_BashSyntaxAndContent(t *testing.T) {
	root := buildSampleTree()
	script := clicore.GenerateBashCompletion(root)

	if !strings.Contains(script, "# bash completion for demo") {
		t.Fatalf("missing header: %s", script)
	}
	if !strings.Contains(script, "_demo_completion()") {
		t.Fatalf("missing completion func: %s", script)
	}
	if !strings.Contains(script, "complete -F _demo_completion demo") {
		t.Fatalf("missing complete registration: %s", script)
	}
	if !strings.Contains(script, "dev qa pm") {
		t.Fatalf("expected enum completion in bash: %s", script)
	}

	// Validate syntax with bash -n if available
	if bashPath, err := exec.LookPath("bash"); err == nil {
		cmd := exec.Command(bashPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash -n failed: %v, output: %s\nScript:\n%s", err, string(out), script)
		}
	}
}

func TestCompletion_ZshSyntaxAndEscaping(t *testing.T) {
	root := buildSampleTree()
	script := clicore.GenerateZshCompletion(root)

	if !strings.Contains(script, "#compdef demo") {
		t.Fatalf("missing header: %s", script)
	}
	if !strings.Contains(script, "compdef _demo demo") {
		t.Fatalf("missing compdef: %s", script)
	}
	// Colon in description must be escaped
	if strings.Contains(script, "Manage employees with quotes ' and colons:") {
		t.Fatalf("unescaped colon found in zsh description: %s", script)
	}
	// Single quotes must be escaped
	if strings.Contains(script, "'Role: primary") {
		t.Fatalf("unescaped colon at boundary in zsh: %s", script)
	}

	// Validate syntax with zsh -n if available
	if zshPath, err := exec.LookPath("zsh"); err == nil {
		cmd := exec.Command(zshPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("zsh -n failed: %v, output: %s\nScript:\n%s", err, string(out), script)
		}
	}
}

func TestCompletion_FishSyntaxAndEscaping(t *testing.T) {
	root := buildSampleTree()
	script := clicore.GenerateFishCompletion(root)

	if !strings.Contains(script, "# fish completion for demo") {
		t.Fatalf("missing header: %s", script)
	}
	if !strings.Contains(script, "complete -c demo -f") {
		t.Fatalf("missing complete disable file: %s", script)
	}
	if !strings.Contains(script, "dev qa pm") {
		t.Fatalf("expected enum in fish: %s", script)
	}

	// Validate syntax with fish -n if available
	if fishPath, err := exec.LookPath("fish"); err == nil {
		cmd := exec.Command(fishPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fish -n failed: %v, output: %s\nScript:\n%s", err, string(out), script)
		}
	}
}

func TestCompletion_AdversarialEscaping(t *testing.T) {
	tmpDir := t.TempDir()
	markerPrefix := filepath.Join(tmpDir, "pwned_")

	// Tree containing adversarial shell syntax in program, command names, flag names, enums, summaries, and descriptions
	subSub := &clicore.Command{
		Name:    "leaf;$(touch " + markerPrefix + "leaf);leaf",
		Summary: "Leaf `whoami` $(touch " + markerPrefix + "leaf_sum)",
	}

	sub := &clicore.Command{
		Name:     "sub\"quote'tick`$(touch " + markerPrefix + "sub);sub",
		Summary:  "Sub with 'single' and \"double\" quotes, :colons, ;semi, $dollars, `ticks`, [brackets], \\backslashes\n\r\t $(touch " + markerPrefix + "sub_sum)",
		Children: []*clicore.Command{subSub},
		Flags: []clicore.Flag{
			{
				Name:        "mode\"flag'tick`$(touch " + markerPrefix + "flag);mode",
				Kind:        clicore.KindString,
				Description: "Mode with 'quotes', :colons, $dollars, `ticks`, [brackets], ;semi, \\backslashes\n\r\t $(touch " + markerPrefix + "flag_desc)",
				Enum: []string{
					"val$(touch " + markerPrefix + "enum1)",
					"val`touch " + markerPrefix + "enum2`",
					"val;touch " + markerPrefix + "enum3",
					"val'single",
					"val\"double",
					"val:colon",
					"val[bracket]",
					"val(paren)",
				},
			},
		},
	}

	root := &clicore.Command{
		Name:     "app;$(touch " + markerPrefix + "app);app",
		Program:  "prog\"app'tick`$(touch " + markerPrefix + "prog);prog",
		Summary:  "Root with 'quotes', \"quotes\", $dollars, `ticks`, ;semi, [brackets]\n\r\t $(touch " + markerPrefix + "root_sum)",
		Children: []*clicore.Command{sub},
		Flags: []clicore.Flag{
			{
				Name:        "global;$(touch " + markerPrefix + "gflag)",
				Kind:        clicore.KindString,
				Description: "Global flag with 'quotes', $money, `exec`, ;semi, [tags]\n\r\t",
				Enum:        []string{"a$(touch " + markerPrefix + "genum)", "b`touch " + markerPrefix + "genum2`"},
			},
		},
	}
	clicore.Prepare(root)

	assertNoMarkers := func(t *testing.T, shellName string) {
		matches, err := filepath.Glob(markerPrefix + "*")
		if err != nil {
			t.Fatalf("failed globbing for markers: %v", err)
		}
		if len(matches) > 0 {
			t.Fatalf("[%s] code execution detected! Created markers: %v", shellName, matches)
		}
	}

	// 1. Bash
	t.Run("Bash", func(t *testing.T) {
		script := clicore.GenerateBashCompletion(root)

		// Assert payload is data in the script
		if !strings.Contains(script, "pwned_") {
			t.Fatalf("expected literal payload in bash script: %s", script)
		}

		bashPath, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("bash not available")
		}

		// Syntax check
		cmd := exec.Command(bashPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash -n failed: %v\nOutput: %s\nScript:\n%s", err, string(out), script)
		}

		// Execution check: sourcing and executing completion function must not run injected commands
		var compFunc string
		for _, line := range strings.Split(script, "\n") {
			if strings.HasPrefix(line, "complete -F ") {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					compFunc = fields[2]
					break
				}
			}
		}
		if compFunc == "" {
			t.Fatalf("could not find completion function in bash script")
		}

		bashTestScript := script + "\n" +
			"_init_completion() { return 1; }\n" +
			"test_invocation() {\n" +
			"    cur=\"$1\"\n" +
			"    shift\n" +
			"    COMP_CWORD=$1\n" +
			"    shift\n" +
			"    COMP_WORDS=(\"$@\")\n" +
			"    " + compFunc + "\n" +
			"}\n" +
			"test_invocation \"\" 1 \"$1\" \"\"\n" +
			"[[ ${#COMPREPLY[@]} -gt 0 ]] || { echo 'empty reply for root'; exit 11; }\n" +
			"test_invocation \"\" 2 \"$1\" \"$2\" \"\"\n" +
			"[[ ${#COMPREPLY[@]} -gt 0 ]] || { echo 'empty reply for sub'; exit 12; }\n" +
			"test_invocation \"\" 3 \"$1\" \"$2\" \"$3\" \"\"\n" +
			"[[ ${#COMPREPLY[@]} -gt 0 ]] || { echo 'empty reply for subSub'; exit 13; }\n" +
			"test_invocation \"\" 2 \"$1\" \"$4\" \"\"\n" +
			"[[ ${#COMPREPLY[@]} -gt 0 ]] || { echo 'empty reply for global flag'; exit 14; }\n" +
			"test_invocation \"\" 3 \"$1\" \"$2\" \"$5\" \"\"\n" +
			"[[ ${#COMPREPLY[@]} -gt 0 ]] || { echo 'empty reply for sub flag'; exit 15; }\n"

		execCmd := exec.Command(bashPath, "-c", bashTestScript, "bash_tester",
			root.Program,
			sub.Name,
			subSub.Name,
			"--"+root.Flags[0].Name,
			"--"+sub.Flags[0].Name,
		)
		execOut, execErr := execCmd.CombinedOutput()
		if execErr != nil {
			t.Fatalf("bash execution failed: %v, output: %s", execErr, string(execOut))
		}
		assertNoMarkers(t, "bash")
	})

	// 2. Zsh
	t.Run("Zsh", func(t *testing.T) {
		script := clicore.GenerateZshCompletion(root)

		// Assert payload is data in the script
		if !strings.Contains(script, "pwned_") {
			t.Fatalf("expected literal payload in zsh script: %s", script)
		}

		zshPath, err := exec.LookPath("zsh")
		if err != nil {
			t.Skip("zsh not available")
		}

		// Syntax check
		cmd := exec.Command(zshPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("zsh -n failed: %v\nOutput: %s\nScript:\n%s", err, string(out), script)
		}

		// Execution check: define dummy compdef and parse/load/execute script
		var compFunc string
		for _, line := range strings.Split(script, "\n") {
			if strings.HasPrefix(line, "compdef ") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					compFunc = fields[1]
					break
				}
			}
		}
		if compFunc == "" {
			t.Fatalf("could not find completion function in zsh script")
		}

		runScript := "compdef() { :; }\n" +
			"described_count=0\n" +
			"_describe() { (( described_count++ )); }\n" +
			script + "\n" +
			"test_invocation() {\n" +
			"    CURRENT=$1\n" +
			"    shift\n" +
			"    words=(\"$@\")\n" +
			"    " + compFunc + "\n" +
			"}\n" +
			"test_invocation 2 \"$1\" \"\"\n" +
			"(( described_count > 0 )) || { echo 'nothing described for root'; exit 21; }\n" +
			"described_count=0\n" +
			"test_invocation 3 \"$1\" \"$2\" \"\"\n" +
			"(( described_count > 0 )) || { echo 'nothing described for sub'; exit 22; }\n" +
			"described_count=0\n" +
			"test_invocation 4 \"$1\" \"$2\" \"$3\" \"\"\n" +
			"(( described_count > 0 )) || { echo 'nothing described for subSub'; exit 23; }\n" +
			"described_count=0\n" +
			"test_invocation 3 \"$1\" \"$4\" \"\"\n" +
			"(( described_count > 0 )) || { echo 'nothing described for global flag'; exit 24; }\n" +
			"described_count=0\n" +
			"test_invocation 4 \"$1\" \"$2\" \"$5\" \"\"\n" +
			"(( described_count > 0 )) || { echo 'nothing described for sub flag'; exit 25; }\n"

		execCmd := exec.Command(zshPath, "-c", runScript, "zsh_tester",
			root.Program,
			sub.Name,
			subSub.Name,
			"--"+root.Flags[0].Name,
			"--"+sub.Flags[0].Name,
		)
		execOut, execErr := execCmd.CombinedOutput()
		if execErr != nil {
			t.Fatalf("zsh execution failed: %v, output: %s", execErr, string(execOut))
		}
		assertNoMarkers(t, "zsh")
	})

	// 3. Fish
	t.Run("Fish", func(t *testing.T) {
		script := clicore.GenerateFishCompletion(root)

		// Assert payload is data in the script
		if !strings.Contains(script, "pwned_") {
			t.Fatalf("expected literal payload in fish script: %s", script)
		}

		fishPath, err := exec.LookPath("fish")
		if err != nil {
			t.Skip("fish not available")
		}

		// Syntax check
		cmd := exec.Command(fishPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fish -n failed: %v\nOutput: %s\nScript:\n%s", err, string(out), script)
		}

		// Execution check: fish parses and registers the completions
		execCmd := exec.Command(fishPath, "-c", script)
		execOut, execErr := execCmd.CombinedOutput()
		if execErr != nil {
			t.Fatalf("fish execution failed: %v, output: %s", execErr, string(execOut))
		}
		assertNoMarkers(t, "fish")
	})
}

func TestCompletion_ValueIntegrity(t *testing.T) {
	root := &clicore.Command{
		Name:    "demo",
		Program: "demo",
		Flags: []clicore.Flag{
			{
				Name: "format",
				Kind: clicore.KindString,
				Enum: []string{"dev", "qa[test]", "pm:lead", "val'single", "val\"double", "val;semi"},
			},
		},
	}
	clicore.Prepare(root)

	t.Run("Bash", func(t *testing.T) {
		bashPath, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("bash not available")
		}
		script := clicore.GenerateBashCompletion(root)

		cmd := exec.Command(bashPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bash -n failed: %v, out: %s", err, string(out))
		}

		runScript := script + "\n" +
			"_init_completion() { return 1; }\n" +
			"cur=\"\"\n" +
			"prev=\"--format\"\n" +
			"COMP_WORDS=(\"demo\" \"--format\" \"\")\n" +
			"COMP_CWORD=2\n" +
			"words=(\"${COMP_WORDS[@]}\")\n" +
			"cword=$COMP_CWORD\n" +
			"_demo_completion\n" +
			"for r in \"${COMPREPLY[@]}\"; do echo \"VAL:$r\"; done\n"

		execCmd := exec.Command(bashPath, "-c", runScript)
		out, err := execCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash execution failed: %v, out: %s", err, string(out))
		}

		outStr := string(out)
		expected := []string{
			"VAL:dev",
			"VAL:qa[test]",
			"VAL:pm:lead",
			"VAL:val'single",
			"VAL:val\"double",
			"VAL:val;semi",
		}
		for _, exp := range expected {
			if !strings.Contains(outStr, exp) {
				t.Errorf("expected %q in bash output, got:\n%s", exp, outStr)
			}
		}

		unexpected := []string{
			"qa\\[test\\]",
			"pm\\:lead",
			"val\\;semi",
		}
		for _, unexp := range unexpected {
			if strings.Contains(outStr, unexp) {
				t.Errorf("unexpected arbitrary backslash in output: %q in:\n%s", unexp, outStr)
			}
		}
	})

	t.Run("Zsh", func(t *testing.T) {
		zshPath, err := exec.LookPath("zsh")
		if err != nil {
			t.Skip("zsh not available")
		}
		script := clicore.GenerateZshCompletion(root)

		cmd := exec.Command(zshPath, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("zsh -n failed: %v, out: %s", err, string(out))
		}
	})
}
