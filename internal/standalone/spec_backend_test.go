package standalone

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/specbackend"
)

const tinySpec = `apiVersion: asao.business/v0
kind: BusinessSpec
metadata: {name: tiny}
resources:
  - entity: people.department
    values: {name: Kho}
`

// With a backend, apply carries out the plan that was reviewed: without --plan-hash or
// --auto-approve it is refused before any Odoo is contacted.
func TestApplyWithABackendNeedsTheReviewedPlan(t *testing.T) {
	t.Setenv(specBackendVariable, "")
	folder := t.TempDir()
	path := filepath.Join(folder, "tiny.yaml")
	if err := os.WriteFile(path, []byte(tinySpec+"backend: "+filepath.Join(folder, "backend")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, envelope := runRoutinesCommand(t, "spec", "apply", "--profile", "nowhere", "--file", path)
	if code != clicore.ExitUsage || errorCode(envelope) != "SPEC_PLAN_HASH_REQUIRED" {
		t.Fatalf("%d %v", code, envelope)
	}
	// "none" sets the spec's backend aside for one run: the profile is what fails next.
	if _, envelope := runRoutinesCommand(t, "spec", "apply", "--profile", "nowhere", "--file", path, "--backend", "none"); errorCode(envelope) == "SPEC_PLAN_HASH_REQUIRED" {
		t.Fatalf("--backend none still asked for a plan hash: %v", envelope)
	}
	if _, err := os.Stat(filepath.Join(folder, "backend")); !os.IsNotExist(err) {
		t.Fatalf("a refused apply wrote the backend: %v", err)
	}
}

// state, runs, pull and unlock read and tend a backend without an Odoo.
func TestBackendStateRunsPullAndUnlock(t *testing.T) {
	t.Setenv(specBackendVariable, "")
	folder := t.TempDir()
	location := filepath.Join(folder, "backend")
	backend, err := specbackend.Open(location)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := backend.CheckTarget(ctx, specbackend.Target{Endpoint: "http://127.0.0.1:58739", Database: "conformance"}); err != nil {
		t.Fatal(err)
	}
	source := specbackend.SpecSource{Name: "tiny", File: "tiny.yaml", Text: tinySpec}
	if _, err := backend.SavePlan(ctx, specbackend.SavedPlan{PlanHash: "sha256:feed", Spec: source}); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	if _, err := backend.SaveRun(ctx, specbackend.Run{ID: "run1", Status: specbackend.RunApplied, PlanHash: "sha256:feed", Spec: source, Started: started}); err != nil {
		t.Fatal(err)
	}
	if err := backend.SaveApplied(ctx, specbackend.Applied{Run: "run1", PlanHash: "sha256:feed", Spec: source}); err != nil {
		t.Fatal(err)
	}
	release, err := backend.Acquire(ctx, specbackend.Lock{ID: "dead1", Operation: "apply", Who: specbackend.Actor{User: "lan", Host: "may-1"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = release

	code, envelope := runRoutinesCommand(t, "spec", "state", "--backend", location)
	data, _ := envelope["data"].(map[string]interface{})
	lock, _ := data["lock"].(map[string]interface{})
	applied, _ := data["applied"].(map[string]interface{})
	if code != clicore.ExitOK || lock["id"] != "dead1" || applied["run"] != "run1" || len(data["runs"].([]interface{})) != 1 {
		t.Fatalf("state: %d %v", code, envelope)
	}
	if code, envelope = runRoutinesCommand(t, "spec", "runs", "--backend", location, "--run", "run1"); code != clicore.ExitOK || envelope["data"].(map[string]interface{})["status"] != "APPLIED" {
		t.Fatalf("runs --run: %d %v", code, envelope)
	}
	pulled := filepath.Join(folder, "pulled.yaml")
	if code, envelope = runRoutinesCommand(t, "spec", "pull", "--backend", location, "--to", pulled); code != clicore.ExitOK {
		t.Fatalf("pull: %v", envelope)
	}
	if text, _ := os.ReadFile(pulled); string(text) != tinySpec {
		t.Fatalf("pulled %q", text)
	}
	if _, envelope = runRoutinesCommand(t, "spec", "pull", "--backend", location, "--to", pulled); errorCode(envelope) != "SPEC_FILE_EXISTS" {
		t.Fatalf("pull over a file: %v", envelope)
	}
	if code, envelope = runRoutinesCommand(t, "spec", "pull", "--backend", location, "--plan", "sha256:feed"); code != clicore.ExitOK ||
		!strings.Contains(envelope["data"].(map[string]interface{})["text"].(string), "people.department") {
		t.Fatalf("pull --plan: %v", envelope)
	}
	if _, envelope = runRoutinesCommand(t, "spec", "unlock", "--backend", location, "--lock-id", "other"); errorCode(envelope) != "SPEC_LOCK_NOT_HELD" {
		t.Fatalf("unlock another lock: %v", envelope)
	}
	if code, envelope = runRoutinesCommand(t, "spec", "unlock", "--backend", location, "--lock-id", "dead1"); code != clicore.ExitOK {
		t.Fatalf("unlock: %v", envelope)
	}
	if held, err := backend.Held(ctx); err != nil || held != nil {
		t.Fatalf("after unlock: %v %v", held, err)
	}
	if _, envelope = runRoutinesCommand(t, "spec", "state"); errorCode(envelope) != "SPEC_BACKEND_REQUIRED" {
		t.Fatalf("state without a backend: %v", envelope)
	}
	if _, envelope = runRoutinesCommand(t, "spec", "state", "--backend", "gs://bucket"); errorCode(envelope) != "SPEC_BACKEND_INVALID" {
		t.Fatalf("a backend of another kind: %v", envelope)
	}
}
