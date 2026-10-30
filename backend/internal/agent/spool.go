package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"
)

// spool keeps batches the server could not take, oldest first, in files.
type spool struct {
	dir string
	max int64
	log *slog.Logger
	seq atomic.Int64
}

func (s *spool) list() []string {
	names, _ := filepath.Glob(filepath.Join(s.dir, "*.json"))
	slices.Sort(names)
	return names
}

func (s *spool) pending() bool { return len(s.list()) > 0 }

// add writes a batch; beyond the size limit the oldest batches are dropped.
func (s *spool) add(body []byte) {
	name := filepath.Join(s.dir, fmt.Sprintf("%020d-%06d.json", time.Now().UnixNano(), s.seq.Add(1)%1_000_000))
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		s.log.Warn("spooling failed; the batch is lost", "error", err.Error())
		return
	}
	if err := os.Rename(tmp, name); err != nil {
		s.log.Warn("spooling failed; the batch is lost", "error", err.Error())
		return
	}
	var total int64
	names := s.list()
	for i := len(names) - 1; i >= 0; i-- {
		if fi, err := os.Stat(names[i]); err == nil {
			total += fi.Size()
			if total > s.max {
				_ = os.Remove(names[i])
				s.log.Warn("spool full; the oldest batch was dropped", "file", filepath.Base(names[i]))
			}
		}
	}
}

// drain sends spooled batches oldest first and stops at the first failure.
func (s *spool) drain(ctx context.Context, sh *shipper) error {
	for _, name := range s.list() {
		body, err := os.ReadFile(name)
		if err != nil {
			_ = os.Remove(name)
			continue
		}
		if err := sh.send(ctx, body); err != nil && !errors.Is(err, errRejected) {
			return err
		}
		_ = os.Remove(name)
	}
	return nil
}
