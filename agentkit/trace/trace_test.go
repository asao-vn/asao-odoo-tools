package trace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAJournalChainsItsEntriesAndFindsAnyChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace", "shop", "trace.jsonl")
	journal := Open(path)
	for i, tool := range []string{"sales_order_list", "sales_order_confirm", "secret_env"} {
		e, err := journal.Append(Entry{Session: "s1", Tool: tool, Confirmation: NotAsked, Status: "OK"})
		if err != nil {
			t.Fatal(err)
		}
		if e.Seq != int64(i+1) || e.Hash == "" || (i > 0) == (e.Prev == "") {
			t.Fatalf("entry %d: %+v", i, e)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	if n, err := Verify(path); n != 3 || err != nil {
		t.Fatalf("verify: %d %v", n, err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	cases := map[string]string{
		"changed": strings.Join([]string{lines[0], strings.Replace(lines[1], "sales_order_confirm", "sales_order_cancel", 1), lines[2]}, "\n"),
		"removed": strings.Join([]string{lines[0], lines[2]}, "\n"),
	}
	for name, text := range cases {
		if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var broken *Broken
		if _, err := Verify(path); !errors.As(err, &broken) || broken.Seq < 2 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAppendsFromManyCallersStayOneChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			journal := Open(path) // a journal per caller, as two servers would have
			for i := 0; i < 25; i++ {
				if _, err := journal.Append(Entry{Session: "s", Tool: "t", Confirmation: NotAsked, Status: "OK"}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if n, err := Verify(path); n != 100 || err != nil {
		t.Fatalf("verify: %d %v", n, err)
	}
}

func TestAMissingJournalIsEmpty(t *testing.T) {
	if entries, err := Read(filepath.Join(t.TempDir(), "none.jsonl")); len(entries) != 0 || err != nil {
		t.Fatalf("%v %v", entries, err)
	}
}
