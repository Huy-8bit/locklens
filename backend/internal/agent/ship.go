package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/locklens/locklens/internal/domain/logs"
)

// maxBatchBytes keeps batches below the server's limit (4 MiB).
const maxBatchBytes = 3 << 20

// errRejected is a batch the server refused for its content (it is not
// sent again).
var errRejected = errors.New("rejected")

type shipper struct{ cfg Config }

// send posts one batch.
func (s *shipper) send(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.Server, "/")+"/api/v1/ingest/logs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "locklens-agent/"+s.cfg.Version)
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: %s", errRejected, msg)
	}
	// 401/403 (token revoked or for another instance), 5xx, proxies: keep
	// the batch and try again later.
	return fmt.Errorf("server answered %s: %s", resp.Status, msg)
}

// batches encodes events into request bodies below maxBatchBytes.
func batches(cfg Config, sources []string, events []logs.Event) [][]byte {
	var out [][]byte
	agent := logs.AgentInfo{Hostname: cfg.Hostname, Version: cfg.Version, Sources: sources}
	for len(events) > 0 {
		n := min(len(events), logs.MaxBatch)
		for {
			raw, err := json.Marshal(logs.Batch{Agent: agent, Events: events[:n]})
			if err == nil && (len(raw) <= maxBatchBytes || n == 1) {
				if len(raw) <= maxBatchBytes {
					out = append(out, raw)
				}
				break
			}
			n = max(1, n/2)
		}
		events = events[n:]
	}
	return out
}
