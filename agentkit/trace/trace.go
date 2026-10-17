// Package trace keeps a local journal of what an agent did through a tool: one line per call, chained by
// hashes so that a line removed or changed afterwards is found (Verify). It records what was asked and what
// came of it (the command line the person saw, whether they confirmed, the status, the error code, the
// references of the records written), never what was read: the values a call returned stay out.
package trace

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Confirmation says how the person's consent was obtained for a call.
const (
	NotAsked    = "not_asked"    // a read, or a write the tool lets run without asking
	Confirmed   = "confirmed"    // the person said yes in the agent host
	ByHost      = "by_host"      // the agent host asked the person itself
	Refused     = "refused"      // declined, timed out, or the host could not ask: nothing ran
	NotRunnable = "not_runnable" // the call was malformed and never reached the person
)

// Entry is one call.
type Entry struct {
	Seq          int64    `json:"seq"`
	At           string   `json:"at"`
	Session      string   `json:"session"`
	Agent        string   `json:"agent,omitempty"`
	Profile      string   `json:"profile,omitempty"`
	Tool         string   `json:"tool"`
	Command      string   `json:"command,omitempty"`
	Access       string   `json:"access,omitempty"`
	Confirmation string   `json:"confirmation"`
	Status       string   `json:"status"`
	ErrorCode    string   `json:"errorCode,omitempty"`
	References   []string `json:"references,omitempty"`
	DataAccess   string   `json:"dataAccess,omitempty"`
	Prev         string   `json:"prev"`
	Hash         string   `json:"hash"`
}

func (e Entry) digest() string {
	e.Hash = ""
	data, _ := json.Marshal(e)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Journal appends entries to one file. Several processes may append to the same file: each append takes
// an exclusive lock on it and chains to the last line as it finds it then.
type Journal struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

// Open prepares a journal at path; the file and its folder are made on the first append, readable by
// their owner only.
func Open(path string) *Journal {
	return &Journal{path: path, now: time.Now}
}

// Path is where the journal is kept.
func (j *Journal) Path() string { return j.path }

// Append chains e to the journal and writes it. Seq, Prev and Hash are set here; At when e has none.
func (j *Journal) Append(e Entry) (Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return Entry{}, err
	}
	file, err := os.OpenFile(j.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return Entry{}, err
	}
	defer file.Close()
	if err := lock(file); err != nil {
		return Entry{}, err
	}
	defer unlock(file)
	last, err := lastEntry(file)
	if err != nil {
		return Entry{}, err
	}
	if e.At == "" {
		e.At = j.now().UTC().Format(time.RFC3339)
	}
	e.Seq, e.Prev = last.Seq+1, last.Hash
	e.Hash = e.digest()
	line, err := json.Marshal(e)
	if err != nil {
		return Entry{}, err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return Entry{}, err
	}
	return e, file.Sync()
}

// lastEntry reads the file's last line; an empty file gives the zero entry.
func lastEntry(file *os.File) (Entry, error) {
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return Entry{}, err
	}
	const tail = 64 << 10
	start := info.Size() - tail
	if start < 0 {
		start = 0
	}
	buffer := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(buffer, start); err != nil && !errors.Is(err, io.EOF) {
		return Entry{}, err
	}
	lines := bytes.Split(bytes.TrimRight(buffer, "\n"), []byte("\n"))
	var last Entry
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		return Entry{}, fmt.Errorf("trace: the last line of %s is not an entry: %w", file.Name(), err)
	}
	return last, nil
}

// Read returns every entry of the journal at path, oldest first; a missing journal is empty.
func Read(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []Entry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for line := 1; scanner.Scan(); line++ {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return out, fmt.Errorf("trace: line %d of %s is not an entry: %w", line, path, err)
		}
		out = append(out, e)
	}
	return out, scanner.Err()
}

// Broken says where a journal stops being the one that was written.
type Broken struct {
	Seq    int64
	Reason string
}

func (b *Broken) Error() string { return fmt.Sprintf("trace: entry %d %s", b.Seq, b.Reason) }

// Verify checks every link of the journal at path: each entry's hash is its own, follows the previous
// entry's, and numbers follow one another. It returns how many entries hold, and a *Broken at the first
// one that does not.
func Verify(path string) (int, error) {
	entries, err := Read(path)
	if err != nil {
		return 0, err
	}
	prev, seq := "", int64(0)
	for i, e := range entries {
		switch {
		case e.Seq != seq+1:
			return i, &Broken{Seq: e.Seq, Reason: fmt.Sprintf("follows entry %d: an entry is missing or out of order", seq)}
		case e.Prev != prev:
			return i, &Broken{Seq: e.Seq, Reason: "does not follow the entry before it: an entry was removed or changed"}
		case e.digest() != e.Hash:
			return i, &Broken{Seq: e.Seq, Reason: "was changed after it was written"}
		}
		prev, seq = e.Hash, e.Seq
	}
	return len(entries), nil
}
