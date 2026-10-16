package specbackend

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The signature of AWS's own worked example (GET /test.txt from examplebucket, Range bytes=0-9) is the
// one AWS publishes for it.
func TestSignatureMatchesAWSWorkedExample(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	request.Header.Set("Range", "bytes=0-9")
	empty := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	request.Header.Set("x-amz-content-sha256", empty)
	request.Header.Set("x-amz-date", "20130524T000000Z")
	got := signature(request, empty, "us-east-1", s3Keys{accessKey: "AKIAIOSFODNN7EXAMPLE", secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
		"20130524T000000Z", "20130524")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got != want {
		t.Fatalf("signature\n got %s\nwant %s", got, want)
	}
}

// fakeS3 is a bucket in memory that checks every request carries a signature for its keys, and
// honours If-None-Match unless told to ignore it, as some stores do.
type fakeS3 struct {
	mu            sync.Mutex
	objects       map[string][]byte
	ignoreIfNone  bool
	requests      []string
	unsigned      int
	authorization string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") || r.Header.Get("x-amz-content-sha256") == "" {
		f.unsigned++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<Error><Code>AccessDenied</Code><Message>unsigned</Message></Error>")
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		prefix := r.URL.Query().Get("prefix")
		var result struct {
			XMLName  xml.Name `xml:"ListBucketResult"`
			Contents []struct {
				Key          string
				LastModified string
				Size         int
			}
			IsTruncated bool
		}
		var keys []string
		for name := range f.objects {
			if strings.HasPrefix(name, prefix) {
				keys = append(keys, name)
			}
		}
		sort.Strings(keys)
		for _, name := range keys {
			result.Contents = append(result.Contents, struct {
				Key          string
				LastModified string
				Size         int
			}{name, "2026-10-16T00:00:00.000Z", len(f.objects[name])})
		}
		_ = xml.NewEncoder(w).Encode(result)
	case r.Method == http.MethodGet:
		data, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code><Message>none</Message></Error>")
			return
		}
		_, _ = w.Write(data)
	case r.Method == http.MethodPut:
		if _, exists := f.objects[key]; exists && r.Header.Get("If-None-Match") == "*" && !f.ignoreIfNone {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, "<Error><Code>PreconditionFailed</Code><Message>exists</Message></Error>")
			return
		}
		data, _ := io.ReadAll(r.Body)
		f.objects[key] = data
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

func openFake(t *testing.T, fake *fakeS3) *Backend {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKID")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRET")
	backend, err := Open("s3://bucket/teams/asao-hq?region=auto&endpoint=" + url.QueryEscape(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

// One apply at a time: a second taker learns who holds the lock, the holder gives it back, and a
// release that names another lock leaves it alone.
func TestLockPlansRunsAndWhatWasApplied(t *testing.T) {
	for name, backend := range map[string]*Backend{"s3": openFake(t, &fakeS3{objects: map[string][]byte{}}), "folder": mustFolder(t)} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			target := Target{Endpoint: "https://odoo.example.vn/", Database: "cong-ty"}
			if err := backend.CheckTarget(ctx, target); err != nil {
				t.Fatal(err)
			}
			if err := backend.CheckTarget(ctx, Target{Endpoint: "https://odoo.example.vn", Database: "cong-ty"}); err != nil {
				t.Fatalf("the same Odoo, written without the slash: %v", err)
			}
			if err := backend.CheckTarget(ctx, Target{Endpoint: "https://odoo.example.vn", Database: "staging"}); !errors.Is(err, ErrOtherTarget) {
				t.Fatalf("another database: %v", err)
			}
			release, err := backend.Acquire(ctx, Lock{Operation: "apply", Spec: "asao-hq", Who: Actor{User: "lan", Host: "may-1"}})
			if err != nil {
				t.Fatal(err)
			}
			var locked *LockedError
			if _, err := backend.Acquire(ctx, Lock{Operation: "apply", Who: Actor{User: "minh"}}); !errors.As(err, &locked) || locked.Holder.Who.User != "lan" {
				t.Fatalf("a second taker: %v", err)
			}
			if err := backend.Release(ctx, "not-mine"); !errors.Is(err, ErrLockNotHeld) {
				t.Fatalf("a release naming another lock: %v", err)
			}
			if err := release(ctx); err != nil {
				t.Fatal(err)
			}
			if held, err := backend.Held(ctx); err != nil || held != nil {
				t.Fatalf("after release: %v %v", held, err)
			}

			where, err := backend.SavePlan(ctx, SavedPlan{PlanHash: "sha256:abc", Spec: SpecSource{Name: "asao-hq", Text: "apiVersion: asao.business/v0\n"},
				Plan: json.RawMessage(`{"planHash":"sha256:abc"}`)})
			if err != nil || !strings.HasSuffix(where, "/plans/abc.json") {
				t.Fatalf("save plan: %s %v", where, err)
			}
			if plan, err := backend.Plan(ctx, "sha256:abc"); err != nil || plan.Spec.Text == "" || plan.Kind != "SpecPlan" {
				t.Fatalf("plan: %+v %v", plan, err)
			}
			first := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
			for index, status := range []string{RunRefused, RunApplied} {
				if _, err := backend.SaveRun(ctx, Run{ID: []string{"r1", "r2"}[index], Status: status, Spec: SpecSource{Name: "asao-hq"},
					Started: first.Add(time.Duration(index) * time.Minute)}); err != nil {
					t.Fatal(err)
				}
			}
			runs, err := backend.Runs(ctx, 0)
			if err != nil || len(runs) != 2 || runs[0].ID != "r2" || runs[0].Status != RunApplied {
				t.Fatalf("runs: %+v %v", runs, err)
			}
			if run, err := backend.Run(ctx, "r1"); err != nil || run.Status != RunRefused {
				t.Fatalf("run r1: %+v %v", run, err)
			}
			if applied, err := backend.LastApplied(ctx); err != nil || applied != nil {
				t.Fatalf("nothing applied yet: %v %v", applied, err)
			}
			if err := backend.SaveApplied(ctx, Applied{Run: "r2", PlanHash: "sha256:abc", Spec: SpecSource{Name: "asao-hq", Text: "x"}}); err != nil {
				t.Fatal(err)
			}
			if applied, err := backend.LastApplied(ctx); err != nil || applied.Run != "r2" || applied.Spec.Text != "x" {
				t.Fatalf("applied: %+v %v", applied, err)
			}
		})
	}
}

func mustFolder(t *testing.T) *Backend {
	t.Helper()
	backend, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

// A store that overwrites what it was asked to create only if absent cannot hold a lock: the lock is
// refused rather than given, and nothing is left behind.
func TestAStoreThatIgnoresCreateIfAbsentHoldsNoLock(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}, ignoreIfNone: true}
	backend := openFake(t, fake)
	if _, err := backend.Acquire(context.Background(), Lock{Operation: "apply"}); !errors.Is(err, ErrNoConditionalWrites) {
		t.Fatalf("got %v", err)
	}
	if _, left := fake.objects["teams/asao-hq/lock.json"]; left {
		t.Fatal("the lock was left behind")
	}
	if fake.unsigned != 0 {
		t.Fatalf("%d unsigned requests", fake.unsigned)
	}
}

func TestLocationsAndCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	if _, err := Open("s3://bucket/x"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("no keys: %v", err)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "AKID")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRET")
	t.Setenv("AWS_ENDPOINT_URL", "")
	t.Setenv("AWS_ENDPOINT_URL_S3", "")
	backend, err := Open("s3://my-bucket/teams/a b?region=ap-southeast-1")
	if err != nil {
		t.Fatal(err)
	}
	store := backend.store.(*s3Store)
	if got := store.objectURL(store.key("lock.json"), nil).String(); got != "https://my-bucket.s3.ap-southeast-1.amazonaws.com/teams/a%20b/lock.json" {
		t.Errorf("AWS address: %s", got)
	}
	if backend.String() != "s3://my-bucket/teams/a b" || strings.Contains(backend.String(), "SECRET") {
		t.Errorf("location: %s", backend.String())
	}
	for _, location := range []string{"", "gs://bucket/x", "s3:///x"} {
		if _, err := Open(location); !errors.Is(err, ErrBadLocation) {
			t.Errorf("%q: %v", location, err)
		}
	}
}
