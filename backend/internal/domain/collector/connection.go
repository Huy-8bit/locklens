package collector

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb"
)

func (w *worker) connect(ctx context.Context) bool {
	if time.Now().Before(w.nextConnect) {
		return false
	}
	now := time.Now().UTC()
	err := func() error {
		plain, err := w.m.box.Open(w.target.DSNCiphertext, []byte(DSNAdditionalData))
		if err != nil {
			return err
		}
		cfg, red, err := config.PoolConfig(string(plain), config.Options{ConnectTimeout: 5 * time.Second, QueryTimeout: 30 * time.Second})
		if err != nil {
			return err
		}
		cfg.MaxConns = int32(w.target.MaxConnections)
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		obs, err := targetdb.Connect(cctx, cfg, red, model.Tool{Name: "locklens", Version: w.m.version})
		if err != nil {
			return err
		}
		var caps observe.Capabilities
		if err := obs.ReadOnly(cctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
			var err error
			caps, err = targetdb.DetectCapabilities(ctx, tx)
			return err
		}); err != nil {
			obs.Close()
			return err
		}
		if caps.VersionNum < targetdb.MinServerVersionNum {
			obs.Close()
			return &targetdb.UnsupportedVersionError{Version: caps.ServerVersion}
		}
		w.obs, w.caps = obs, &caps
		return nil
	}()
	w.state.mu.Lock()
	w.state.connectAt = &now
	if err != nil {
		msg := sanitizeError(err)
		w.lastConnectErr = &msg
		w.state.connected = false
		w.state.connectError = &msg
		w.state.mu.Unlock()
		w.connectFailures++
		backoff := time.Duration(w.target.IntervalSeconds) * time.Second
		for i := 1; i < w.connectFailures && backoff < maxReconnectBackoff; i++ {
			backoff *= 2
		}
		w.nextConnect = time.Now().Add(min(backoff, maxReconnectBackoff))
		w.m.log.Warn("target connection failed", "target_id", w.target.ID, "target", w.target.Name, "error", msg)
		w.m.events.Publish("target.status", map[string]any{"target_id": w.target.ID})
		return false
	}
	w.state.connected = true
	w.state.connectError = nil
	w.state.caps = w.caps
	w.state.mu.Unlock()
	w.lastConnectErr = nil
	w.connectFailures = 0
	w.refreshEnabled()
	return true
}

func (w *worker) close() {
	// The current load bucket is stored with the ticks it has; its average
	// stays correct because it is divided by those ticks.
	w.flushLoad()
	w.closeDatabases(nil)
	if w.obs != nil {
		w.obs.Close()
		w.obs = nil
	}
}

func (w *worker) disconnect(err error) {
	w.close()
	msg := sanitizeError(err)
	w.lastConnectErr = &msg
	w.state.mu.Lock()
	w.state.connected = false
	w.state.connectError = &msg
	w.state.mu.Unlock()
	w.connectFailures = 1
	w.nextConnect = time.Now().Add(time.Duration(w.target.IntervalSeconds) * time.Second)
}

// isConnectionError reports failures that mean the connection or the server
// is gone, after which the worker reconnects.
func isConnectionError(err error) bool {
	var ce *pgconn.ConnectError
	var ne net.Error
	switch {
	case errors.As(err, &ce), errors.Is(err, pgconn.ErrConnClosed), errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return true
	case errors.As(err, &ne):
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// admin_shutdown, crash_shutdown, cannot_connect_now
		return pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03"
	}
	msg := err.Error()
	return strings.Contains(msg, "conn closed") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "acquire connection")
}

// sanitizeError returns a single-line error message for storage and the UI.
// Errors from targetdb.Connect and the collectors are already redacted; this
// additionally bounds length and strips line breaks.
func sanitizeError(err error) string {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	if hint := targetdb.Hint(err); hint != "" {
		msg += " (hint: " + hint + ")"
	}
	return msg
}

// DSNAdditionalData binds encrypted DSNs to their purpose.
const DSNAdditionalData = "locklens-target-dsn-v1"
