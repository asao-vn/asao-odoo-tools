package standalone

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo/review"
)

// A team's own review checks and the person's verdicts (DEC-2026-0181), kept per profile beside its packs:
//   <profiles dir>/review/<profile>/checks/<ID>.json, files whose own cases passed when added and pass again
//   each time review run loads them;
//   <profiles dir>/review/<profile>/verdicts.jsonl, one line per verdict on a finding's key.

func reviewDir(profile string) (string, error) {
	path, err := localprofile.DefaultPath()
	if err != nil {
		return "", err
	}
	if profile == "" || profile == "." || profile == ".." || strings.ContainsAny(profile, `/\`) {
		return "", fmt.Errorf("profile name %q cannot name a directory", profile)
	}
	return filepath.Join(filepath.Dir(path), "review", profile), nil
}

func readCheckFile(path string) (review.CheckFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return review.CheckFile{}, clicore.NewError(clicore.CategoryConfig, "CHECK_FILE_UNREADABLE", fmt.Sprintf("cannot read %s: %v", path, err))
	}
	file, err := review.ParseCheck(data)
	if err != nil {
		return review.CheckFile{}, clicore.UsageError("CHECK_REFUSED", "%v", err)
	}
	return file, nil
}

// loadProfileChecks registers the profile's own checks; one that no longer passes its cases is skipped,
// and named.
func loadProfileChecks(profile string) []string {
	dir, err := reviewDir(profile)
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "checks"))
	var skipped []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file, err := readCheckFile(filepath.Join(dir, "checks", entry.Name()))
		if err == nil {
			err = review.RegisterCheck(file)
		}
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", entry.Name(), err))
		}
	}
	return skipped
}

func profileName(invocation *clicore.Invocation, config programConfig) (string, error) {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return "", err
	}
	return selection.Profile.Name, nil
}

type verdict struct {
	Key     string `json:"key"`
	Check   string `json:"check"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
	At      string `json:"at"`
}

// readVerdicts returns the last verdict on each key.
func readVerdicts(profile string) (map[string]verdict, error) {
	dir, err := reviewDir(profile)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(dir, "verdicts.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]verdict{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	out := map[string]verdict{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var v verdict
		if json.Unmarshal(scanner.Bytes(), &v) == nil && v.Key != "" {
			out[v.Key] = v
		}
	}
	return out, scanner.Err()
}

func reviewTeamCommands(config programConfig) []*clicore.Command {
	fileFlag := clicore.Flag{Name: "file", Kind: clicore.KindString, Required: true, Description: "A declared check, odoo-cli.check/v1."}
	testCheck := local(&clicore.Command{
		Name:    "test-check",
		Summary: "Check a declared check file and run its own cases, without an Odoo.",
		Description: `What a check's author runs before a pull request, and what CI runs on every file: the format, the read
operation it names, its matcher and its cases (one set it must flag, one it must leave alone).`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{fileFlag},
		Examples:    []string{config.name + " review test-check --file checks/X-REFUNDS-PILING-UP.json --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			file, err := readCheckFile(invocation.String("file"))
			if err != nil {
				return nil, err
			}
			results := make([]map[string]interface{}, 0, len(file.Cases))
			passed := true
			for _, c := range file.Cases {
				found, err := file.TestCase(c)
				row := map[string]interface{}{"case": c.Name, "findings": len(found), "passed": err == nil}
				if err != nil {
					row["error"], passed = err.Error(), false
				}
				results = append(results, row)
			}
			if !passed {
				return nil, clicore.UsageError("CHECK_CASES_FAILED", "check %s v%d: a case did not answer as expected", file.ID, file.Version).
					WithContextValue("cases", results)
			}
			return map[string]interface{}{"check": file.ID, "version": file.Version, "cases": results, "passed": true}, nil
		},
	})
	addCheck := local(&clicore.Command{
		Name:    "add-check",
		Summary: "Add a declared check to the profile's review, once its own cases pass.",
		Description: `The check runs in review run beside the built-in ones. Its id may not be a built-in check's; a file with the
id of one already added replaces it.`,
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags:       []clicore.Flag{profileFlag, fileFlag},
		Examples:    []string{config.name + " review add-check --profile my-odoo --file X-REFUNDS-PILING-UP.json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := profileName(invocation, config)
			if err != nil {
				return nil, err
			}
			file, err := readCheckFile(invocation.String("file"))
			if err != nil {
				return nil, err
			}
			if err := review.RegisterCheck(file); err != nil {
				return nil, clicore.UsageError("CHECK_REFUSED", "%v", err)
			}
			dir, err := reviewDir(name)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			data, _ := os.ReadFile(invocation.String("file"))
			if err := os.MkdirAll(filepath.Join(dir, "checks"), 0o700); err != nil {
				return nil, clicore.InternalError(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "checks", file.ID+".json"), data, 0o600); err != nil {
				return nil, clicore.InternalError(err)
			}
			return map[string]interface{}{"profile": name, "check": file.ID, "version": file.Version, "added": true}, nil
		},
	})
	removeCheck := local(&clicore.Command{
		Name:        "remove-check",
		Summary:     "Remove a check the profile added.",
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags:       []clicore.Flag{profileFlag, {Name: "check", Kind: clicore.KindString, Required: true, Description: "The check's id."}},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := profileName(invocation, config)
			if err != nil {
				return nil, err
			}
			dir, err := reviewDir(name)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			id := strings.ToUpper(strings.TrimSpace(invocation.String("check")))
			if err := os.Remove(filepath.Join(dir, "checks", id+".json")); err != nil {
				return nil, clicore.NewError(clicore.CategoryNotFound, "CHECK_NOT_FOUND", fmt.Sprintf("profile %s added no check %s", name, id))
			}
			return map[string]interface{}{"profile": name, "check": id, "removed": true}, nil
		},
	})
	verdictCommand := local(&clicore.Command{
		Name:    "verdict",
		Summary: "Say whether a finding was real or a false alarm; it is kept for the check's precision.",
		Description: `The finding is named by its key (review run gives each one). A later verdict on the same key replaces the
earlier one. Kept on this machine, per profile.`,
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags: []clicore.Flag{profileFlag,
			{Name: "finding", Kind: clicore.KindString, Required: true, Description: "The finding's key, e.g. O23-1a2b3c4d5e6f."},
			{Name: "real", Kind: clicore.KindBool, Description: "It was a real problem."},
			{Name: "false-alarm", Kind: clicore.KindBool, Description: "It was not a problem."},
			{Name: "note", Kind: clicore.KindString, Description: "Why, in a few words."}},
		Examples: []string{config.name + " review verdict --profile my-odoo --finding O23-1a2b3c4d5e6f --false-alarm --note 'team lunch'"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := profileName(invocation, config)
			if err != nil {
				return nil, err
			}
			real, _ := invocation.Bool("real")
			falseAlarm, _ := invocation.Bool("false-alarm")
			if real == falseAlarm {
				return nil, clicore.UsageError("INVALID_FLAG_VALUE", "pass exactly one of --real and --false-alarm")
			}
			key := strings.TrimSpace(invocation.String("finding"))
			idx := strings.LastIndex(key, "-")
			if idx < 1 || len(key)-idx-1 != 12 {
				return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--finding %q is not a finding's key (CHECK-xxxxxxxxxxxx)", key)
			}
			check := key[:idx]
			v := verdict{Key: key, Check: check, Verdict: map[bool]string{true: "real", false: "false"}[real],
				Note: strings.TrimSpace(invocation.String("note")), At: time.Now().UTC().Format(time.RFC3339)}
			dir, err := reviewDir(name)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, clicore.InternalError(err)
			}
			handle, err := os.OpenFile(filepath.Join(dir, "verdicts.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			defer handle.Close()
			line, _ := json.Marshal(v)
			if _, err := handle.Write(append(line, '\n')); err != nil {
				return nil, clicore.InternalError(err)
			}
			return map[string]interface{}{"profile": name, "verdict": v}, nil
		},
	})
	precision := local(&clicore.Command{
		Name:    "precision",
		Summary: "Each check's share of real findings, from the verdicts given on this profile.",
		Description: `Precision is real / (real + false alarm) over the findings that have a verdict. The false alarms are listed
by key: each is a look-alike case its check's file should gain.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{personalProfileFlag()},
		Examples:    []string{config.name + " review precision --profile my-odoo --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := profileName(invocation, config)
			if err != nil {
				return nil, err
			}
			verdicts, err := readVerdicts(name)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			type tally struct {
				Check       string   `json:"check"`
				Real        int      `json:"real"`
				FalseAlarms int      `json:"falseAlarms"`
				Precision   float64  `json:"precision"`
				FalseKeys   []string `json:"falseAlarmKeys,omitempty"`
			}
			byCheck := map[string]*tally{}
			for _, v := range verdicts {
				t := byCheck[v.Check]
				if t == nil {
					t = &tally{Check: v.Check}
					byCheck[v.Check] = t
				}
				if v.Verdict == "real" {
					t.Real++
				} else {
					t.FalseAlarms++
					t.FalseKeys = append(t.FalseKeys, v.Key)
				}
			}
			out := make([]tally, 0, len(byCheck))
			for _, t := range byCheck {
				t.Precision = float64(t.Real) / float64(t.Real+t.FalseAlarms)
				sort.Strings(t.FalseKeys)
				out = append(out, *t)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Check < out[j].Check })
			return map[string]interface{}{"profile": name, "checks": out, "verdicts": len(verdicts)}, nil
		},
	})
	return []*clicore.Command{testCheck, addCheck, removeCheck, verdictCommand, precision}
}

func personalProfileFlag() clicore.Flag {
	flag := profileFlag
	flag.PersonOnly = true
	return flag
}

// annotateVerdicts marks each finding of a report with the person's verdict on its key, when there is one.
func annotateVerdicts(report *review.Report, profile string) {
	verdicts, err := readVerdicts(profile)
	if err != nil || len(verdicts) == 0 {
		return
	}
	for i := range report.Findings {
		if v, ok := verdicts[report.Findings[i].Key]; ok {
			report.Findings[i].Verdict = v.Verdict
		}
	}
}
