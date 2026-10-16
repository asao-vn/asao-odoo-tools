// Package specbackend is where a team keeps what it does to one Odoo with "spec": the lock that lets
// one apply run at a time, the plans saved for review, the record of every apply, and the spec last
// applied, which a teammate can pull back. It lives in an S3 bucket (or R2, MinIO, B2) or in a shared
// folder.
//
// It is not Terraform's state. A spec names its records by their own fields and every plan reads
// them from Odoo itself, so there is nothing to keep in step with Odoo and nothing to lose. What a
// team needs from a shared place is the rest: not applying twice at once, applying the plan that was
// reviewed, and knowing who changed what and when.
//
// Who is a label (the operating-system user and host), not an identity: this package authenticates
// nobody, and the bucket's own permissions decide who can write. Approval by a colleague whose
// identity is checked belongs to the governed asao CLI, or to the reviews of the git host the spec
// lives in.
package specbackend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	// ErrBadLocation is a backend location that is neither s3://bucket/prefix nor a folder.
	ErrBadLocation = errors.New("specbackend: not a backend location")
	// ErrNoCredentials is an S3 backend with no keys to sign with.
	ErrNoCredentials = errors.New("specbackend: no S3 credentials")
	// ErrNotFound is an object, or a bucket, that is not there.
	ErrNotFound = errors.New("specbackend: not found")
	// ErrExists is an object Create found already there.
	ErrExists = errors.New("specbackend: already exists")
	// ErrUnreachable is a store that could not be reached.
	ErrUnreachable = errors.New("specbackend: the store could not be reached")
	// ErrDenied is a store that refused the credentials or the request.
	ErrDenied = errors.New("specbackend: the store refused the request")
	// ErrNoConditionalWrites is a store that overwrote an object it was asked to create only if
	// absent: a lock there would not keep two applies apart, so none is taken.
	ErrNoConditionalWrites = errors.New("specbackend: the store does not honour create-if-absent, so it cannot hold a lock")
	// ErrOtherTarget is a backend that belongs to another Odoo.
	ErrOtherTarget = errors.New("specbackend: this backend belongs to another Odoo")
	// ErrLockNotHeld is an unlock naming a lock that is not the one held.
	ErrLockNotHeld = errors.New("specbackend: that lock is not the one held")
)

// Store keeps a backend's objects, by name relative to the backend's location.
type Store interface {
	String() string
	Get(ctx context.Context, name string) ([]byte, error)
	Put(ctx context.Context, name string, data []byte) error
	// Create writes an object only when there is none, in one step the store guarantees; ErrExists
	// otherwise.
	Create(ctx context.Context, name string, data []byte) error
	Delete(ctx context.Context, name string) error
	List(ctx context.Context, folder string) ([]Object, error)
}

// Object is one stored object.
type Object struct {
	Name     string    `json:"name"`
	Modified time.Time `json:"modified"`
	Size     int64     `json:"size"`
}

// Open reads a backend location: s3://bucket/prefix (with ?region=, ?endpoint=, ?path_style=), a
// file:// URL or a folder path.
func Open(location string) (*Backend, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return nil, fmt.Errorf("%w: empty", ErrBadLocation)
	}
	var store Store
	switch {
	case strings.HasPrefix(location, "s3://"):
		parsed, err := url.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadLocation, err)
		}
		if store, err = openS3(parsed); err != nil {
			return nil, err
		}
	case strings.HasPrefix(location, "file://"):
		parsed, err := url.Parse(location)
		if err != nil || parsed.Path == "" {
			return nil, fmt.Errorf("%w: file:///absolute/folder", ErrBadLocation)
		}
		store = &folderStore{root: filepath.FromSlash(parsed.Path)}
	case strings.Contains(location, "://"):
		return nil, fmt.Errorf("%w: %s (s3:// or a folder)", ErrBadLocation, location)
	default:
		absolute, err := filepath.Abs(location)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadLocation, err)
		}
		store = &folderStore{root: absolute}
	}
	return &Backend{store: store, now: time.Now}, nil
}

// Backend coordinates what a team does to one Odoo with spec.
type Backend struct {
	store Store
	now   func() time.Time
}

// New wraps a store, for tests and for callers that bring their own.
func New(store Store, now func() time.Time) *Backend {
	if now == nil {
		now = time.Now
	}
	return &Backend{store: store, now: now}
}

// String is the backend's location, without credentials.
func (b *Backend) String() string { return b.store.String() }

// Target is the Odoo a backend belongs to.
type Target struct {
	Endpoint string `json:"endpoint"`
	Database string `json:"database"`
}

func (t Target) same(other Target) bool {
	return strings.TrimRight(t.Endpoint, "/") == strings.TrimRight(other.Endpoint, "/") && t.Database == other.Database
}

// Actor is who did something, as a label: the operating-system user and host. It is not checked.
type Actor struct {
	User string `json:"user"`
	Host string `json:"host"`
}

// CurrentActor is the label of whoever runs this process.
func CurrentActor() Actor {
	actor := Actor{User: os.Getenv("USER")}
	if current, err := user.Current(); err == nil && current.Username != "" {
		actor.User = current.Username
	}
	actor.Host, _ = os.Hostname()
	return actor
}

// CheckTarget makes sure the backend belongs to this Odoo, recording it on first use.
func (b *Backend) CheckTarget(ctx context.Context, target Target) error {
	recorded, err := b.Target(ctx)
	if errors.Is(err, ErrNotFound) {
		data, _ := json.MarshalIndent(target, "", " ")
		if err := b.store.Create(ctx, "target.json", data); err != nil && !errors.Is(err, ErrExists) {
			return err
		}
		if recorded, err = b.Target(ctx); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if !recorded.same(target) {
		return fmt.Errorf("%w: %s holds %s database %s, not %s database %s", ErrOtherTarget, b.store, recorded.Endpoint, recorded.Database, target.Endpoint, target.Database)
	}
	return nil
}

// Target reads the Odoo the backend belongs to.
func (b *Backend) Target(ctx context.Context) (Target, error) {
	var target Target
	err := b.read(ctx, "target.json", &target)
	return target, err
}

func (b *Backend) read(ctx context.Context, name string, into interface{}) error {
	data, err := b.store.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("specbackend: %s/%s is not what this tool wrote: %w", b.store, name, err)
	}
	return nil
}

func (b *Backend) write(ctx context.Context, name string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		return err
	}
	return b.store.Put(ctx, name, data)
}

// ---- the lock -----------------------------------------------------------------------------------

// Lock is held while one apply runs.
type Lock struct {
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	Spec      string    `json:"spec,omitempty"`
	PlanHash  string    `json:"planHash,omitempty"`
	Who       Actor     `json:"who"`
	Since     time.Time `json:"since"`
	Tool      string    `json:"tool,omitempty"`
}

// LockedError is a lock somebody else holds.
type LockedError struct{ Holder Lock }

func (e *LockedError) Error() string {
	return fmt.Sprintf("specbackend: %s@%s holds the lock since %s (%s, lock %s)", e.Holder.Who.User, e.Holder.Who.Host,
		e.Holder.Since.Format(time.RFC3339), e.Holder.Operation, e.Holder.ID)
}

// NewID is a short random id for a lock or a run.
func NewID() string {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// Acquire takes the lock, or reports who holds it. It then asks the store to create the lock again
// and expects a refusal: a store that overwrites instead would let two applies run together, and the
// lock is given back with ErrNoConditionalWrites.
func (b *Backend) Acquire(ctx context.Context, lock Lock) (func(context.Context) error, error) {
	if lock.ID == "" {
		lock.ID = NewID()
	}
	if lock.Since.IsZero() {
		lock.Since = b.now().UTC()
	}
	data, _ := json.MarshalIndent(lock, "", " ")
	if err := b.store.Create(ctx, "lock.json", data); err != nil {
		if !errors.Is(err, ErrExists) {
			return nil, err
		}
		var holder Lock
		if readErr := b.read(ctx, "lock.json", &holder); readErr != nil {
			return nil, fmt.Errorf("specbackend: the lock is held, and could not be read: %w", readErr)
		}
		return nil, &LockedError{Holder: holder}
	}
	if err := b.store.Create(ctx, "lock.json", data); !errors.Is(err, ErrExists) {
		_ = b.store.Delete(ctx, "lock.json")
		if err != nil {
			return nil, err
		}
		return nil, ErrNoConditionalWrites
	}
	return func(ctx context.Context) error { return b.Release(ctx, lock.ID) }, nil
}

// Held reads the lock; nil when nobody holds it.
func (b *Backend) Held(ctx context.Context) (*Lock, error) {
	var lock Lock
	err := b.read(ctx, "lock.json", &lock)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lock, nil
}

// Release gives the lock back, only when it is the one named: a lock somebody else took after a
// forced unlock stays theirs.
func (b *Backend) Release(ctx context.Context, id string) error {
	held, err := b.Held(ctx)
	if err != nil {
		return err
	}
	if held == nil {
		return nil
	}
	if held.ID != id {
		return fmt.Errorf("%w: %s is held, not %s", ErrLockNotHeld, held.ID, id)
	}
	return b.store.Delete(ctx, "lock.json")
}

// ---- plans, runs and what was applied -----------------------------------------------------------

// SpecSource is the spec as its author wrote it, and the files it names, by hash.
type SpecSource struct {
	Name  string    `json:"name"`
	File  string    `json:"file"`
	Text  string    `json:"text"`
	Files []FileRef `json:"files,omitempty"`
}

// FileRef is a file a spec names ({file: home.xml}), by its hash.
type FileRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

// SavedPlan is a plan kept for review.
type SavedPlan struct {
	Kind     string          `json:"kind"`
	PlanHash string          `json:"planHash"`
	Spec     SpecSource      `json:"spec"`
	Target   Target          `json:"target"`
	Who      Actor           `json:"who"`
	At       time.Time       `json:"at"`
	Tool     string          `json:"tool,omitempty"`
	Plan     json.RawMessage `json:"plan"`
}

func planName(hash string) string {
	_, hexPart, found := strings.Cut(hash, ":")
	if !found {
		hexPart = hash
	}
	return "plans/" + hexPart + ".json"
}

// SavePlan keeps a plan for review, under its hash. It returns where.
func (b *Backend) SavePlan(ctx context.Context, plan SavedPlan) (string, error) {
	plan.Kind = "SpecPlan"
	if plan.At.IsZero() {
		plan.At = b.now().UTC()
	}
	name := planName(plan.PlanHash)
	return b.store.String() + "/" + name, b.write(ctx, name, plan)
}

// Plan reads a saved plan by its hash.
func (b *Backend) Plan(ctx context.Context, hash string) (SavedPlan, error) {
	var plan SavedPlan
	err := b.read(ctx, planName(hash), &plan)
	return plan, err
}

// Run statuses.
const (
	RunApplied = "APPLIED"
	// RunPartial is an apply with --partial that left UNSUPPORTED or AMBIGUOUS steps out: what ran
	// is recorded, and the spec is not kept as the one last applied in full.
	RunPartial = "PARTIAL"
	RunFailed  = "FAILED"
	RunRefused = "REFUSED"
)

// Run is the record of one apply.
type Run struct {
	Kind             string          `json:"kind"`
	ID               string          `json:"id"`
	Status           string          `json:"status"`
	Reason           string          `json:"reason,omitempty"`
	PlanHash         string          `json:"planHash,omitempty"`
	Spec             SpecSource      `json:"spec"`
	Target           Target          `json:"target"`
	Who              Actor           `json:"who"`
	Started          time.Time       `json:"started"`
	Finished         time.Time       `json:"finished"`
	Tool             string          `json:"tool,omitempty"`
	Summary          map[string]int  `json:"summary,omitempty"`
	Plan             json.RawMessage `json:"plan,omitempty"`
	PlanAfterModules json.RawMessage `json:"planAfterModules,omitempty"`
	Report           json.RawMessage `json:"report,omitempty"`
}

// RunSummary is a run as a listing shows it.
type RunSummary struct {
	ID       string    `json:"id"`
	Status   string    `json:"status"`
	Spec     string    `json:"spec"`
	PlanHash string    `json:"planHash,omitempty"`
	Who      Actor     `json:"who"`
	Started  time.Time `json:"started"`
	Object   string    `json:"object"`
}

func runName(run Run) string {
	return "runs/" + run.Started.UTC().Format("20060102T150405Z") + "-" + run.ID + ".json"
}

// SaveRun records an apply. It returns where.
func (b *Backend) SaveRun(ctx context.Context, run Run) (string, error) {
	run.Kind = "SpecRun"
	if run.ID == "" {
		run.ID = NewID()
	}
	name := runName(run)
	return b.store.String() + "/" + name, b.write(ctx, name, run)
}

// Runs lists the recorded applies, the latest first; limit 0 lists them all.
func (b *Backend) Runs(ctx context.Context, limit int) ([]RunSummary, error) {
	objects, err := b.store.List(ctx, "runs")
	if err != nil {
		return nil, err
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Name > objects[j].Name })
	if limit > 0 && len(objects) > limit {
		objects = objects[:limit]
	}
	out := make([]RunSummary, 0, len(objects))
	for _, object := range objects {
		var run Run
		if err := b.read(ctx, object.Name, &run); err != nil {
			return nil, err
		}
		out = append(out, RunSummary{ID: run.ID, Status: run.Status, Spec: run.Spec.Name, PlanHash: run.PlanHash, Who: run.Who,
			Started: run.Started, Object: b.store.String() + "/" + object.Name})
	}
	return out, nil
}

// Run reads one recorded apply by its id.
func (b *Backend) Run(ctx context.Context, id string) (Run, error) {
	objects, err := b.store.List(ctx, "runs")
	if err != nil {
		return Run{}, err
	}
	for _, object := range objects {
		if strings.HasSuffix(object.Name, "-"+id+".json") {
			var run Run
			err := b.read(ctx, object.Name, &run)
			return run, err
		}
	}
	return Run{}, fmt.Errorf("%w: run %s", ErrNotFound, id)
}

// Applied is the spec last applied in full, which a teammate can pull back.
type Applied struct {
	Kind     string     `json:"kind"`
	Run      string     `json:"run"`
	PlanHash string     `json:"planHash"`
	Spec     SpecSource `json:"spec"`
	Target   Target     `json:"target"`
	Who      Actor      `json:"who"`
	At       time.Time  `json:"at"`
	Tool     string     `json:"tool,omitempty"`
}

// SaveApplied records the spec an apply carried out in full.
func (b *Backend) SaveApplied(ctx context.Context, applied Applied) error {
	applied.Kind = "SpecApplied"
	if applied.At.IsZero() {
		applied.At = b.now().UTC()
	}
	return b.write(ctx, "applied.json", applied)
}

// LastApplied reads the spec last applied in full; nil when none was.
func (b *Backend) LastApplied(ctx context.Context) (*Applied, error) {
	var applied Applied
	err := b.read(ctx, "applied.json", &applied)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &applied, nil
}
