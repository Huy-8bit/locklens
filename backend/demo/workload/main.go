// Package main provides a small CRUD API and continuous synthetic client load
// for the local LockLens PostgreSQL lab only.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultDSN = "postgres://demo_app:demo-app-pw@primary:5432/locklens_demo?sslmode=disable"

type item struct {
	ID        int64     `json:"id"`
	Payload   string    `json:"payload"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type api struct {
	db *pgxpool.Pool
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := pgxpool.ParseConfig(env("DATABASE_URL", defaultDSN))
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 8
	cfg.ConnConfig.RuntimeParams["application_name"] = "backend-simulator"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("connect to lab primary: %w", err)
	}
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.backend_workload_items (
			id bigserial PRIMARY KEY,
			payload text NOT NULL,
			version bigint NOT NULL DEFAULT 1,
			updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
		)`); err != nil {
		return fmt.Errorf("create backend workload table: %w", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO public.backend_workload_items (payload)
		SELECT 'seed-' || n FROM generate_series(1, 100) AS n
		WHERE NOT EXISTS (SELECT 1 FROM public.backend_workload_items)`); err != nil {
		return fmt.Errorf("seed backend workload table: %w", err)
	}

	mux := http.NewServeMux()
	a := &api{db: db}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /items", a.list)
	mux.HandleFunc("POST /items", a.create)
	mux.HandleFunc("GET /items/{id}", a.get)
	mux.HandleFunc("PUT /items/{id}", a.update)
	mux.HandleFunc("DELETE /items/{id}", a.delete)

	addr := env("LISTEN_ADDR", "0.0.0.0:8091")
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("backend simulator listening on %s", addr)
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	port := addr[strings.LastIndex(addr, ":"):]
	baseURL := "http://127.0.0.1" + port
	interval, err := time.ParseDuration(env("WORKLOAD_INTERVAL", "2s"))
	if err != nil || interval < 100*time.Millisecond {
		return errors.New("WORKLOAD_INTERVAL must be at least 100ms")
	}
	concurrency, err := strconv.Atoi(env("WORKLOAD_CONCURRENCY", "2"))
	if err != nil || concurrency < 1 || concurrency > 8 {
		return errors.New("WORKLOAD_CONCURRENCY must be between 1 and 8")
	}
	var workers sync.WaitGroup
	for worker := 1; worker <= concurrency; worker++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			runClient(ctx, id, baseURL, interval)
		}(worker)
	}

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		stop()
		workers.Wait()
		return fmt.Errorf("serve backend API: %w", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down backend API: %w", err)
	}
	workers.Wait()
	return nil
}

func (a *api) list(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `
		SELECT id, payload, version, updated_at
		FROM public.backend_workload_items ORDER BY id DESC LIMIT 50`)
	if err != nil {
		http.Error(w, "list items failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	items := make([]item, 0, 50)
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Payload, &it.Version, &it.UpdatedAt); err != nil {
			http.Error(w, "read items failed", http.StatusInternalServerError)
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "read items failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *api) create(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Payload string `json:"payload"`
	}
	if err := decodeJSON(w, r, &request); err != nil || strings.TrimSpace(request.Payload) == "" {
		http.Error(w, "payload is required", http.StatusBadRequest)
		return
	}
	var it item
	err := a.db.QueryRow(r.Context(), `
		INSERT INTO public.backend_workload_items (payload)
		VALUES ($1) RETURNING id, payload, version, updated_at`, request.Payload).
		Scan(&it.ID, &it.Payload, &it.Version, &it.UpdatedAt)
	if err != nil {
		http.Error(w, "create item failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, it)
}

func (a *api) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var it item
	err := a.db.QueryRow(r.Context(), `
		SELECT id, payload, version, updated_at
		FROM public.backend_workload_items WHERE id = $1`, id).
		Scan(&it.ID, &it.Payload, &it.Version, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "get item failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (a *api) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var request struct {
		Payload string `json:"payload"`
	}
	if err := decodeJSON(w, r, &request); err != nil || strings.TrimSpace(request.Payload) == "" {
		http.Error(w, "payload is required", http.StatusBadRequest)
		return
	}
	var it item
	err := a.db.QueryRow(r.Context(), `
		UPDATE public.backend_workload_items
		SET payload = $2, version = version + 1, updated_at = clock_timestamp()
		WHERE id = $1 RETURNING id, payload, version, updated_at`, id, request.Payload).
		Scan(&it.ID, &it.Payload, &it.Version, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "update item failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (a *api) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM public.backend_workload_items WHERE id = $1`, id)
	if err != nil {
		http.Error(w, "delete item failed", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func runClient(ctx context.Context, workerID int, baseURL string, interval time.Duration) {
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		if err := workloadCycle(ctx, client, baseURL, workerID); err != nil && ctx.Err() == nil {
			log.Printf("workload worker %d: %v", workerID, err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func workloadCycle(ctx context.Context, client *http.Client, baseURL string, workerID int) error {
	payload := fmt.Sprintf(`{"payload":"worker-%d-%d"}`, workerID, time.Now().UnixNano())
	var created item
	if err := requestJSON(ctx, client, http.MethodPost, baseURL+"/items", payload, http.StatusCreated, &created); err != nil {
		return fmt.Errorf("POST item: %w", err)
	}
	var fetched item
	if err := requestJSON(ctx, client, http.MethodGet, fmt.Sprintf("%s/items/%d", baseURL, created.ID), "", http.StatusOK, &fetched); err != nil {
		return fmt.Errorf("SELECT item: %w", err)
	}
	update := fmt.Sprintf(`{"payload":"updated-%d-%d"}`, workerID, time.Now().UnixNano())
	if err := requestJSON(ctx, client, http.MethodPut, fmt.Sprintf("%s/items/%d", baseURL, created.ID), update, http.StatusOK, &fetched); err != nil {
		return fmt.Errorf("UPDATE item: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/items/%d", baseURL, created.ID), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("DELETE item: unexpected HTTP status %d", resp.StatusCode)
	}
	return nil
}

func requestJSON(ctx context.Context, client *http.Client, method, url, body string, wantStatus int, result any) error {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("request must contain exactly one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
