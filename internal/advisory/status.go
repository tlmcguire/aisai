package advisory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Status describes the state of the advisory source: whether it is the live
// OSV service or a local snapshot, and how large that snapshot is.
//
// The offline count and timestamp come from the database itself rather than a
// sidecar manifest, because the officially published database is a plain
// directory of per-package JSON files. That keeps the air-gapped path honest:
// if the files are there, the count is real.
type Status struct {
	// Mode is "online" or "offline".
	Mode string `json:"mode"`
	// Available reports whether the source can currently be queried.
	Available bool `json:"available"`
	// UpdatedAt is the most recent modification time in the database. It is
	// zero in online mode.
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
	// Count is the number of advisories in the database. It is zero in
	// online mode.
	Count int `json:"count"`
	// Error explains why the source is not available. It is empty when the
	// source works.
	//
	// It exists because the offline mode must be able to say "the database
	// you configured is not there" without the tool falling back to the
	// network behind the operator's back.
	Error string `json:"error,omitempty"`
}

// Status reports the current state of the advisory source.
//
// An offline database that is configured but missing is reported as
// unavailable, with the reason. It is never reported as online: a client
// that was pointed at a local snapshot must not quietly start talking to
// api.osv.dev, because that is exactly the leak the offline mode exists to
// prevent.
func (c *Client) Status() Status {
	if c.offline {
		if !dirExists(c.opts.OfflineDBPath) {
			return Status{
				Mode:      "offline",
				Available: false,
				Error: "the offline advisory database " + c.opts.OfflineDBPath +
					" does not exist; the client stays offline rather than querying the network",
			}
		}
		st := Status{Mode: "offline", Available: true}
		st.Count, st.UpdatedAt = scanDatabase(c.opts.OfflineDBPath)
		return st
	}
	// Online availability is not probed here: a health check that performs a
	// network request would make a status call slow and would fail on an
	// air-gapped machine for no informational gain. The mode itself is the
	// useful signal.
	return Status{Mode: "online", Available: true}
}

// scanDatabase counts the advisories in an offline database and finds the
// most recent modification time.
//
// The count is the number of vulnerability records across all package files,
// not the number of files: a single package file can hold many advisories.
func scanDatabase(root string) (int, time.Time) {
	var (
		updated time.Time
		total   int
	)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A single unreadable entry must not abort the scan.
			return nil
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if info.ModTime().After(updated) {
			updated = info.ModTime()
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var rec osvResponse
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil
		}
		total += len(rec.Vulns)
		return nil
	})
	if err != nil {
		return total, updated
	}
	return total, updated
}
