package standalone

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

// The timer that calls "routines due": a systemd user timer on Linux, a launchd agent on macOS, a
// scheduled task on Windows, and a crontab line where none of those is there. Its name is
// odoo-cli-routines, apart from the asao-erp SDK's own timer.

const (
	routinesUnit   = "odoo-cli-routines"
	routinesLaunch = "vn.asao.odoo-cli-routines"
)

func routinesInstallCommand(config programConfig, fileFlag clicore.Flag) *clicore.Command {
	return local(&clicore.Command{
		Name:    "install",
		Summary: "Put \"routines due\" on this computer's timer: systemd, launchd, Task Scheduler or cron.",
		Description: `Installs a timer that calls "routines due --file <file>" every --every minutes, as the person
who runs it. It keeps PATH (where the agents are), XDG_CONFIG_HOME (where the profiles are),
` + providerendpoint.AllowPrivateEnvVar + ` and ` + localprofile.CredentialFileVariable + ` (a path, never a credential): credentials stay in
the keyring or the credential file. --print shows what would be installed and changes nothing.`,
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Flags: []clicore.Flag{fileFlag,
			{Name: "every", Kind: clicore.KindInt, Default: "15", Description: "Minutes between two checks of what is due."},
			{Name: "print", Kind: clicore.KindBool, Description: "Show what would be installed; change nothing."},
		},
		Examples: []string{config.name + " routines install --file ~/routines.toml", config.name + " routines install --print"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return installRoutinesTimer(ctx, invocation)
		},
	})
}

func routinesUninstallCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "uninstall",
		Summary:    "Remove the timer \"routines install\" put in place.",
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Examples:   []string{config.name + " routines uninstall"},
		Run: func(ctx context.Context, _ *clicore.Invocation) (interface{}, error) {
			return uninstallRoutinesTimer(ctx)
		},
	})
}

// timerPlan is what an install writes and runs.
type timerPlan struct {
	Scheduler string            `json:"scheduler"`
	Every     int               `json:"everyMinutes"`
	Command   []string          `json:"command"`
	Log       string            `json:"log"`
	Files     map[string]string `json:"files,omitempty"`
	Runs      [][]string        `json:"runs,omitempty"`
	Crontab   string            `json:"crontabLine,omitempty"`
}

// timerEnvironment is what the timer needs from this shell: where the agents and odoo-cli are, where
// the profiles are, and paths odoo-cli reads. Never a credential.
func timerEnvironment() [][2]string {
	keep := [][2]string{{"PATH", os.Getenv("PATH")}}
	for _, name := range []string{"XDG_CONFIG_HOME", providerendpoint.AllowPrivateEnvVar, localprofile.CredentialFileVariable, localprofile.SelectorVariable} {
		if value := os.Getenv(name); value != "" {
			keep = append(keep, [2]string{name, value})
		}
	}
	return keep
}

// systemdQuote quotes one word of a unit file: inside double quotes, with % written %% (a unit's
// specifiers) and, on an Exec line, $ written $$ (its variables).
func systemdQuote(word string, exec bool) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(word)
	if exec {
		escaped = strings.ReplaceAll(escaped, "$", "$$")
	}
	return `"` + escaped + `"`
}

func systemdUnits(command []string, environment [][2]string, log string, every int) (string, string) {
	var service strings.Builder
	service.WriteString("[Unit]\nDescription=odoo-cli routines that are due\n\n[Service]\nType=oneshot\n")
	for _, pair := range environment {
		service.WriteString("Environment=" + systemdQuote(pair[0]+"="+pair[1], false) + "\n")
	}
	words := make([]string, len(command))
	for index, word := range command {
		words[index] = systemdQuote(word, true)
	}
	service.WriteString("ExecStart=" + strings.Join(words, " ") + "\n")
	service.WriteString("StandardOutput=append:" + log + "\nStandardError=append:" + log + "\n")
	timer := fmt.Sprintf("[Unit]\nDescription=Check odoo-cli routines every %d minutes\n\n[Timer]\nOnCalendar=*:0/%d\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", every, every)
	return service.String(), timer
}

func xmlText(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}

func launchdPlist(command []string, environment [][2]string, log string, every int) string {
	var plist strings.Builder
	plist.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n" +
		`<plist version="1.0"><dict>` + "\n<key>Label</key><string>" + routinesLaunch + "</string>\n<key>ProgramArguments</key><array>")
	for _, word := range command {
		plist.WriteString("<string>" + xmlText(word) + "</string>")
	}
	plist.WriteString("</array>\n<key>EnvironmentVariables</key><dict>")
	for _, pair := range environment {
		plist.WriteString("<key>" + xmlText(pair[0]) + "</key><string>" + xmlText(pair[1]) + "</string>")
	}
	plist.WriteString(fmt.Sprintf("</dict>\n<key>StartInterval</key><integer>%d</integer>\n", every*60))
	plist.WriteString("<key>StandardOutPath</key><string>" + xmlText(log) + "</string>\n")
	plist.WriteString("<key>StandardErrorPath</key><string>" + xmlText(log) + "</string>\n</dict></plist>\n")
	return plist.String()
}

// shellQuote quotes one word for a POSIX shell (a crontab line).
func shellQuote(word string) string {
	if word != "" && strings.Trim(word, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-") == "" {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'"'"'`) + "'"
}

func cronLine(command []string, environment [][2]string, log string, every int) string {
	var line strings.Builder
	line.WriteString(fmt.Sprintf("*/%d * * * * ", every))
	for _, pair := range environment {
		line.WriteString(pair[0] + "=" + shellQuote(pair[1]) + " ")
	}
	words := make([]string, len(command))
	for index, word := range command {
		words[index] = shellQuote(word)
	}
	line.WriteString(strings.Join(words, " ") + " >> " + shellQuote(log) + " 2>&1 # " + routinesUnit)
	return line.String()
}

// windowsCommandLine joins arguments the way a Windows program splits them.
func windowsCommandLine(command []string) string {
	words := make([]string, len(command))
	for index, word := range command {
		if word != "" && !strings.ContainsAny(word, " \t\"") {
			words[index] = word
			continue
		}
		words[index] = `"` + strings.ReplaceAll(word, `"`, `\"`) + `"`
	}
	return strings.Join(words, " ")
}

// systemdUserFolder is where the user's systemd manager reads units: its own XDG_CONFIG_HOME, not
// this shell's, which may point odoo-cli at another profile folder.
func systemdUserFolder(ctx context.Context) string {
	shown, _ := exec.CommandContext(ctx, "systemctl", "--user", "show-environment").Output()
	for _, line := range strings.Split(string(shown), "\n") {
		if value, ok := strings.CutPrefix(line, "XDG_CONFIG_HOME="); ok && value != "" {
			return filepath.Join(value, "systemd", "user")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func systemdUserRunning(ctx context.Context) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	err := exec.CommandContext(ctx, "systemctl", "--user", "is-system-running").Run()
	if err == nil {
		return true
	}
	// 1 is "degraded" or "starting": the manager is there.
	exited, ok := err.(*exec.ExitError)
	return ok && exited.ExitCode() == 1
}

func installRoutinesTimer(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
	file, err := loadRoutinesFile(invocation)
	if err != nil {
		return nil, err
	}
	every, err := invocation.Int("every", 15)
	if err != nil {
		return nil, err
	}
	if every < 1 || every > 1440 {
		return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--every is minutes, 1 to 1440").WithContextValue("flag", "every")
	}
	printOnly, err := invocation.Bool("print")
	if err != nil {
		return nil, err
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, clicore.InternalError(err)
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	command := []string{binary, "routines", "due", "--file", file.Source}
	environment := timerEnvironment()
	log := filepath.Join(file.Output, "scheduler.log")
	plan := timerPlan{Every: every, Command: command, Log: log, Files: map[string]string{}}
	switch {
	case runtime.GOOS == "linux" && systemdUserRunning(ctx):
		folder := systemdUserFolder(ctx)
		service, timer := systemdUnits(command, environment, log, every)
		plan.Scheduler = "systemd"
		plan.Files[filepath.Join(folder, routinesUnit+".service")] = service
		plan.Files[filepath.Join(folder, routinesUnit+".timer")] = timer
		plan.Runs = [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", routinesUnit + ".timer"}}
	case runtime.GOOS == "darwin":
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, "Library", "LaunchAgents", routinesLaunch+".plist")
		plan.Scheduler = "launchd"
		plan.Files[path] = launchdPlist(command, environment, log, every)
		plan.Runs = [][]string{{"launchctl", "unload", path}, {"launchctl", "load", path}}
	case runtime.GOOS == "windows":
		plan.Scheduler = "schtasks"
		plan.Runs = [][]string{{"schtasks", "/Create", "/F", "/SC", "MINUTE", "/MO", fmt.Sprint(every), "/TN", routinesUnit, "/TR", windowsCommandLine(command)}}
	default:
		if _, err := exec.LookPath("crontab"); err != nil {
			return nil, clicore.NewError(clicore.CategoryConfig, "NO_SCHEDULER", "no scheduler found: systemd, launchd, Task Scheduler or cron").
				WithRemediation("Call \"routines due --file " + file.Source + "\" from the scheduler you have.")
		}
		plan.Scheduler = "cron"
		plan.Crontab = cronLine(command, environment, log, every)
	}
	if printOnly {
		return plan, nil
	}
	if err := os.MkdirAll(file.Output, 0o700); err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "ROUTINES_OUTPUT_UNWRITABLE", err.Error())
	}
	for path, content := range plan.Files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, clicore.NewError(clicore.CategoryConfig, "TIMER_NOT_INSTALLED", err.Error())
		}
		if err := writeLocalFile(path, []byte(content)); err != nil {
			return nil, clicore.NewError(clicore.CategoryConfig, "TIMER_NOT_INSTALLED", err.Error())
		}
	}
	for index, run := range plan.Runs {
		output, err := exec.CommandContext(ctx, run[0], run[1:]...).CombinedOutput()
		// launchctl unload fails when nothing was loaded yet; that is the first install.
		if err != nil && !(plan.Scheduler == "launchd" && index == 0) {
			return nil, clicore.NewError(clicore.CategoryConfig, "TIMER_NOT_INSTALLED", fmt.Sprintf("%s: %v: %s", strings.Join(run, " "), err, strings.TrimSpace(string(output))))
		}
	}
	if plan.Scheduler == "cron" {
		if err := replaceCronLine(ctx, plan.Crontab); err != nil {
			return nil, clicore.NewError(clicore.CategoryConfig, "TIMER_NOT_INSTALLED", err.Error())
		}
	}
	return map[string]interface{}{"installed": plan.Scheduler, "everyMinutes": every, "command": command, "log": log,
		"remove": "routines uninstall"}, nil
}

// replaceCronLine puts line in the user's crontab in place of the one an earlier install wrote; an
// empty line only removes it. It reports whether a line was there.
func replaceCronLine(ctx context.Context, line string) error {
	current, _ := exec.CommandContext(ctx, "crontab", "-l").Output()
	var kept []string
	for _, existing := range strings.Split(strings.TrimRight(string(current), "\n"), "\n") {
		if existing != "" && !strings.HasSuffix(existing, "# "+routinesUnit) {
			kept = append(kept, existing)
		}
	}
	if line != "" {
		kept = append(kept, line)
	}
	content := strings.Join(kept, "\n")
	if content != "" {
		content += "\n"
	}
	install := exec.CommandContext(ctx, "crontab", "-")
	install.Stdin = strings.NewReader(content)
	if output, err := install.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func uninstallRoutinesTimer(ctx context.Context) (interface{}, error) {
	var removed []string
	switch {
	case runtime.GOOS == "linux" && systemdUserRunning(ctx):
		_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", routinesUnit+".timer").Run()
		folder := systemdUserFolder(ctx)
		for _, suffix := range []string{".service", ".timer"} {
			path := filepath.Join(folder, routinesUnit+suffix)
			if os.Remove(path) == nil {
				removed = append(removed, path)
			}
		}
		_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
	case runtime.GOOS == "darwin":
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, "Library", "LaunchAgents", routinesLaunch+".plist")
		_ = exec.CommandContext(ctx, "launchctl", "unload", path).Run()
		if os.Remove(path) == nil {
			removed = append(removed, path)
		}
	case runtime.GOOS == "windows":
		if exec.CommandContext(ctx, "schtasks", "/Delete", "/F", "/TN", routinesUnit).Run() == nil {
			removed = append(removed, "scheduled task "+routinesUnit)
		}
	}
	if _, err := exec.LookPath("crontab"); err == nil && runtime.GOOS != "windows" {
		current, _ := exec.CommandContext(ctx, "crontab", "-l").Output()
		if strings.Contains(string(current), "# "+routinesUnit) {
			if err := replaceCronLine(ctx, ""); err != nil {
				return nil, clicore.NewError(clicore.CategoryConfig, "TIMER_NOT_REMOVED", err.Error())
			}
			removed = append(removed, "crontab line "+routinesUnit)
		}
	}
	return map[string]interface{}{"removed": removed, "count": len(removed)}, nil
}
