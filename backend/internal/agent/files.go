package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/lib/pglog"
)

// Limits of reading.
const (
	maxReadPerPoll = 8 << 20  // bytes read from one file per poll
	maxUnparsed    = 16 << 20 // an incomplete record larger than this is dropped
	idleFlush      = 2 * time.Second
	stateFile      = "positions.json"
)

// position is where reading of a file stands.
type position struct {
	Offset int64 `json:"offset"`
	// Size is the file's size at the last read.
	Size int64 `json:"size"`
}

// tailed is a file being followed.
type tailed struct {
	path    string
	format  string
	info    os.FileInfo
	pos     position
	buf     []byte // read but not yet parsed (an incomplete record)
	stderr  *pglog.StderrReader
	lastNew time.Time
}

// files follows the log files of the configured paths.
type files struct {
	cfg    Config
	prefix *pglog.Prefix
	tails  map[string]*tailed
	saved  map[string]position
	first  bool // no positions were saved: the first start
}

func openFiles(cfg Config, prefix *pglog.Prefix) (*files, error) {
	f := &files{cfg: cfg, prefix: prefix, tails: map[string]*tailed{}, saved: map[string]position{}}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, stateFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
		f.first = true
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(raw, &f.saved); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// match lists the log files of the configured paths.
func (f *files) match() []string {
	var out []string
	for _, p := range f.cfg.Paths {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			entries, _ := os.ReadDir(p)
			for _, e := range entries {
				if n := e.Name(); !e.IsDir() && (strings.HasSuffix(n, ".log") || strings.HasSuffix(n, ".csv") || strings.HasSuffix(n, ".json")) {
					out = append(out, filepath.Join(p, n))
				}
			}
			continue
		}
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sources are the files followed (for the server's agent status).
func (f *files) sources() []string {
	out := make([]string, 0, len(f.tails))
	for p := range f.tails {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// poll reads what was added to every file and returns its events.
func (f *files) poll() ([]logs.Event, error) {
	var out []logs.Event
	var errs []error
	seen := map[string]bool{}
	for _, path := range f.match() {
		seen[path] = true
		t, ok := f.tails[path]
		if !ok {
			t = f.start(path)
			if t == nil {
				continue
			}
			f.tails[path] = t
		}
		ev, err := f.read(t)
		out = append(out, ev...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	// Files removed (rotated away): finish them.
	for path, t := range f.tails {
		if !seen[path] {
			out = append(out, f.flush(t)...)
			delete(f.tails, path)
			delete(f.saved, path)
		}
	}
	f.first = false
	return out, errors.Join(errs...)
}

// start begins following a file: at its saved position, at its end for a
// file that existed at the first start (unless FromStart), or at its start
// for a file that appeared later (a new log file after rotation).
func (f *files) start(path string) *tailed {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	t := &tailed{path: path, info: fi, format: f.cfg.Format}
	if t.format == "" || t.format == "auto" {
		t.format = pglog.FormatOf(path)
	}
	if t.format == pglog.FormatStderr {
		t.stderr = pglog.NewStderrReader(f.prefix)
	}
	switch p, ok := f.saved[path]; {
	case ok && p.Offset <= fi.Size():
		t.pos = p
	case f.first && !f.cfg.FromStart:
		t.pos = position{Offset: fi.Size(), Size: fi.Size()}
	}
	return t
}

// read reads the new part of a file.
func (f *files) read(t *tailed) ([]logs.Event, error) {
	fi, err := os.Stat(t.path)
	if err != nil {
		return nil, err
	}
	if fi.Size() < t.pos.Offset || !os.SameFile(fi, t.info) {
		// Truncated or replaced: read the new content from its start.
		ev := f.flush(t)
		t.pos, t.buf, t.info = position{}, nil, fi
		return ev, nil
	}
	t.info = fi
	if fi.Size() == t.pos.Offset {
		if len(t.buf) > 0 || t.stderr != nil {
			if time.Since(t.lastNew) > idleFlush {
				return f.flush(t), nil
			}
		}
		return nil, nil
	}
	file, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(t.pos.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	chunk, err := io.ReadAll(io.LimitReader(file, maxReadPerPoll))
	if err != nil {
		return nil, err
	}
	t.pos.Offset += int64(len(chunk))
	t.pos.Size = fi.Size()
	t.lastNew = time.Now()
	t.buf = append(t.buf, chunk...)
	return f.parse(t), nil
}

// parse turns the complete records in the buffer into events.
func (f *files) parse(t *tailed) []logs.Event {
	var recs []pglog.Record
	switch t.format {
	case pglog.FormatCSV:
		var used int
		recs, used = pglog.ParseCSV(t.buf)
		t.buf = t.buf[used:]
	default:
		end := bytes.LastIndexByte(t.buf, '\n')
		if end < 0 {
			break
		}
		for _, line := range strings.Split(string(t.buf[:end]), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if t.format == pglog.FormatJSON {
				if r, err := pglog.ParseJSON([]byte(line)); err == nil {
					recs = append(recs, r)
				}
				continue
			}
			recs = append(recs, t.stderr.Add(line)...)
		}
		t.buf = t.buf[end+1:]
	}
	if len(t.buf) > maxUnparsed {
		f.cfg.Log.Warn("dropping an unreadable part of a log file", "file", t.path, "bytes", len(t.buf))
		t.buf = nil
	}
	t.buf = slices.Clone(t.buf) // do not keep the whole chunk alive
	return classify(recs)
}

// flush ends the record being assembled (stderr).
func (f *files) flush(t *tailed) []logs.Event {
	if t.stderr == nil {
		return nil
	}
	return classify(t.stderr.Flush())
}

func (f *files) flushAll() []logs.Event {
	var out []logs.Event
	for _, t := range f.tails {
		out = append(out, f.flush(t)...)
	}
	return out
}

// save keeps the read positions. A position is after what was read; the
// part of a record not yet complete is read again after a restart.
func (f *files) save() error {
	for path, t := range f.tails {
		p := t.pos
		p.Offset -= int64(len(t.buf))
		f.saved[path] = p
	}
	raw, err := json.Marshal(f.saved)
	if err != nil {
		return err
	}
	tmp := filepath.Join(f.cfg.StateDir, stateFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(f.cfg.StateDir, stateFile))
}

func classify(recs []pglog.Record) []logs.Event {
	var out []logs.Event
	for _, r := range recs {
		if e, ok := logs.Classify(r); ok {
			out = append(out, e)
		}
	}
	return out
}
