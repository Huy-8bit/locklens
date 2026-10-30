// Package cluster lets several LockLens servers share one monitoring store.
//
// Every server registers itself and renews a heartbeat. The collection of
// each instance is held by one server through a lease ("target:<id>"); the
// servers split the instances evenly and a server that stops renewing its
// leases loses them, so the others take its instances over within about a
// minute. Jobs that must run once per cluster (retention, rollups,
// baselines, the optimizer, notification reminders) hold a lease too
// ("job:<name>"). A message bus over PostgreSQL LISTEN/NOTIFY carries
// browser events and requests between the servers.
//
// A single server is a cluster of one: it holds every lease.
package cluster

import (
	"cmp"
	"context"
	"hash/fnv"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Timing. A target lease outlives two collector syncs (every 15 s), so a
// slow sync does not lose it.
const (
	heartbeatEvery = 5 * time.Second
	// ServerTTL is how long a server without a heartbeat still counts.
	ServerTTL      = 20 * time.Second
	targetLeaseTTL = 45 * time.Second
	jobLeaseTTL    = 30 * time.Second
	jobRenewEvery  = 10 * time.Second
)

const targetPrefix = "target:"

// Node is this server's membership.
type Node struct {
	store    Store
	log      *slog.Logger
	self     repository.Server
	mu       sync.RWMutex
	servers  []repository.Server
	alive    int
	assigned map[int64]bool
}

// New returns the node of server id (unique per running server).
func New(store Store, id, hostname, version string, log *slog.Logger) *Node {
	return &Node{store: store, log: log, alive: 1, assigned: map[int64]bool{},
		self: repository.Server{ID: id, Hostname: hostname, Version: version, StartedAt: time.Now().UTC()}}
}

// ID is the server's ID.
func (n *Node) ID() string { return n.self.ID }

// Alone reports whether no other live server shares the store.
func (n *Node) Alone() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.alive <= 1
}

// Servers returns the servers seen at the last heartbeat.
func (n *Node) Servers() []repository.Server {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return slices.Clone(n.servers)
}

// Owns reports whether this server collects a target.
func (n *Node) Owns(targetID int64) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.assigned[targetID]
}

// Run renews the heartbeat until ctx ends, then releases this server's
// target leases so the others take them over at once.
func (n *Node) Run(ctx context.Context) {
	n.beat(ctx)
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			n.releaseAll()
			return
		case <-t.C:
			n.beat(ctx)
		}
	}
}

func (n *Node) beat(ctx context.Context) {
	bctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := n.store.Heartbeat(bctx, n.self); err != nil {
		n.log.Warn("cluster heartbeat failed", "error", err.Error())
		return
	}
	servers, err := n.store.Servers(bctx, ServerTTL)
	if err != nil {
		n.log.Warn("cluster: listing servers failed", "error", err.Error())
		return
	}
	alive := 0
	for _, s := range servers {
		if s.Alive {
			alive++
		}
	}
	n.mu.Lock()
	n.servers, n.alive = servers, max(alive, 1)
	n.mu.Unlock()
}

func (n *Node) releaseAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n.mu.Lock()
	ids := n.assigned
	n.assigned = map[int64]bool{}
	n.mu.Unlock()
	for id := range ids {
		_ = n.store.ReleaseLease(ctx, leaseName(id), n.self.ID)
	}
}

// Assign decides which of the wanted targets this server collects: it
// renews its leases up to an even share of them (ceil(targets / live
// servers)), releases the rest, and takes free or expired ones while it has
// fewer. The collector manager calls it on every sync.
func (n *Node) Assign(ctx context.Context, wanted []int64) (map[int64]bool, error) {
	leases, err := n.store.Leases(ctx, targetPrefix)
	if err != nil {
		return nil, err
	}
	n.mu.RLock()
	alive := n.alive
	n.mu.RUnlock()
	share := (len(wanted) + alive - 1) / alive
	held := map[int64]repository.Lease{}
	for _, l := range leases {
		if id, ok := leaseTarget(l.Name); ok {
			held[id] = l
		}
	}
	want := map[int64]bool{}
	var mine, free []int64
	for _, id := range wanted {
		want[id] = true
		l, ok := held[id]
		switch {
		case ok && l.Holder == n.self.ID:
			mine = append(mine, id)
		case !ok || !l.Live:
			free = append(free, id)
		}
	}
	slices.Sort(mine)
	// Servers try free targets in different orders, so they rarely race
	// for the same one.
	slices.SortFunc(free, func(a, b int64) int { return cmp.Compare(n.spread(a), n.spread(b)) })

	owned := map[int64]bool{}
	for _, id := range mine {
		if len(owned) >= share {
			_ = n.store.ReleaseLease(ctx, leaseName(id), n.self.ID) // rebalance: another server takes it
			continue
		}
		ok, _, err := n.store.HoldLease(ctx, leaseName(id), n.self.ID, targetLeaseTTL)
		if err != nil {
			return nil, err
		}
		if ok {
			owned[id] = true
		}
	}
	for _, id := range free {
		if len(owned) >= share {
			break
		}
		ok, _, err := n.store.HoldLease(ctx, leaseName(id), n.self.ID, targetLeaseTTL)
		if err != nil {
			return nil, err
		}
		if ok {
			owned[id] = true
		}
	}
	// Leases of targets that are no longer collected (disabled, deleted).
	for id, l := range held {
		if l.Holder == n.self.ID && !want[id] {
			_ = n.store.ReleaseLease(ctx, leaseName(id), n.self.ID)
		}
	}
	n.mu.Lock()
	n.assigned = owned
	n.mu.Unlock()
	return owned, nil
}

// spread orders targets differently per server.
func (n *Node) spread(id int64) uint32 {
	h := fnv.New32a()
	h.Write([]byte(n.self.ID + "/" + strconv.FormatInt(id, 10)))
	return h.Sum32()
}

// Singleton runs job while this server holds its lease and stops it when
// the lease is lost, until ctx ends. Only one server runs a job at a time.
func (n *Node) Singleton(ctx context.Context, job string, run func(context.Context)) {
	name := "job:" + job
	var cur *runningJob
	t := time.NewTicker(jobRenewEvery)
	defer t.Stop()
	for {
		hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
		held, _, err := n.store.HoldLease(hctx, name, n.self.ID, jobLeaseTTL)
		hcancel()
		switch {
		case err != nil && ctx.Err() == nil:
			n.log.Warn("cluster: job lease failed", "job", job, "error", err.Error())
			cur = cur.stop() // without a renewed lease another server may start it
		case held && cur == nil:
			cur = startJob(ctx, run)
			n.log.Info("cluster: running job here", "job", job)
		case !held && cur != nil:
			cur = cur.stop()
			n.log.Info("cluster: job moved to another server", "job", job)
		}
		select {
		case <-ctx.Done():
			cur.stop()
			rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = n.store.ReleaseLease(rctx, name, n.self.ID)
			rcancel()
			return
		case <-t.C:
		}
	}
}

// runningJob is a job this server runs.
type runningJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startJob(ctx context.Context, run func(context.Context)) *runningJob {
	jctx, cancel := context.WithCancel(ctx)
	j := &runningJob{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(j.done)
		run(jctx)
	}()
	return j
}

// stop stops the job and waits for it; it returns nil (no job).
func (j *runningJob) stop() *runningJob {
	if j != nil {
		j.cancel()
		<-j.done
	}
	return nil
}

func leaseName(id int64) string { return targetPrefix + strconv.FormatInt(id, 10) }

func leaseTarget(name string) (int64, bool) {
	s, ok := strings.CutPrefix(name, targetPrefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil
}
