//go:build windows

package trace

import "os"

// On Windows the in-process mutex is the only lock: two servers appending to one journal at the same
// moment may chain to the same entry, which Verify then reports.
func lock(*os.File) error   { return nil }
func unlock(*os.File) error { return nil }
