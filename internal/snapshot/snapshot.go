// Package snapshot reads and writes snapshot files.
//
// Writes are crash-safe: the JSON is written to a temporary file in the
// destination directory, synced, and only then moved into place, so the
// destination is either absent, the previous file, or the complete new file.
package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/locklens/locklens/internal/model"
)

// ErrExists is returned when the destination exists and force is false.
var ErrExists = errors.New("file already exists (use --force to overwrite)")

// UnsupportedVersionError is returned for a schema version this build cannot
// read.
type UnsupportedVersionError struct {
	Found int
}

func (e *UnsupportedVersionError) Error() string {
	if e.Found == 0 {
		return "not a LockLens snapshot: schema_version is missing"
	}
	return fmt.Sprintf("unsupported snapshot schema_version %d (this LockLens reads version %d); upgrade LockLens to read it",
		e.Found, model.SchemaVersion)
}

// InvalidError is returned when a file is not valid snapshot JSON.
type InvalidError struct {
	Path string
	Err  error
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("%s is not a valid snapshot file: %v", e.Path, e.Err)
}

func (e *InvalidError) Unwrap() error { return e.Err }

// Save writes snap to path. Without force it refuses to replace an existing
// file, and the check is atomic (no window between "exists?" and "write").
// The file is created with mode 0600 because it may contain query text.
func Save(path string, snap *model.Snapshot, force bool) (err error) {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	data = append(data, '\n')

	if !force {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%s: %w", path, ErrExists)
		}
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync snapshot: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close snapshot: %w", err)
	}

	if force {
		if err = os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("move snapshot into place: %w", err)
		}
		return nil
	}
	// A hard link fails with EEXIST if the destination appeared meanwhile,
	// which makes "do not overwrite" race-free.
	if err = os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s: %w", path, ErrExists)
		}
		// Some file systems do not support hard links. Fall back to
		// check-then-rename, which has a small race window.
		if _, statErr := os.Lstat(path); statErr == nil {
			err = fmt.Errorf("%s: %w", path, ErrExists)
			return err
		}
		if err = os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("move snapshot into place: %w", err)
		}
		return nil
	}
	_ = os.Remove(tmpName)
	return nil
}

// Load reads and validates a snapshot file.
func Load(path string) (*model.Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Decode(f, path)
}

// Decode reads a snapshot from r. name is used in error messages.
//
// Compatibility: files with the current schema version are accepted; unknown
// fields are ignored so that additive changes within a version stay readable.
// Any other version is rejected rather than guessed at.
func Decode(r io.Reader, name string) (*model.Snapshot, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, &InvalidError{Path: name, Err: describeJSONError(data, err)}
	}
	if probe.SchemaVersion != model.SchemaVersion {
		return nil, &UnsupportedVersionError{Found: probe.SchemaVersion}
	}
	var snap model.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, &InvalidError{Path: name, Err: describeJSONError(data, err)}
	}
	if snap.ServerTime.IsZero() {
		return nil, &InvalidError{Path: name, Err: errors.New("server_time is missing")}
	}
	return &snap, nil
}

// describeJSONError adds a line number to syntax errors.
func describeJSONError(data []byte, err error) error {
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		line := 1 + bytes.Count(data[:min(int(syn.Offset), len(data))], []byte("\n"))
		return fmt.Errorf("line %d: %v", line, syn)
	}
	var typ *json.UnmarshalTypeError
	if errors.As(err, &typ) {
		return fmt.Errorf("field %q has the wrong type: %v", typ.Field, typ)
	}
	return err
}
