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

	"github.com/locklens/locklens/internal/lib/obs"
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

// Node is this server's membership in the cluster: heartbeats, the targets it collects, and the jobs it runs.
type Node interface {
	// ID is the server's ID.
	ID() string
	// Alone reports whether no other live server shares the store.
	Alone() bool
	// Servers returns the servers seen at the last heartbeat.
	Servers() []repository.Server
	// Owns reports whether this server collects a target.
	Owns(targetID int64) bool
	// Run renews the heartbeat until ctx ends, then releases this server's
	// target leases so the others take them over at once.
	Run(ctx context.Context)
	// Assign decides which of the wanted targets this server collects: it
	// renews its leases up to an even share of them (ceil(targets / live
	// servers)), releases the rest, and takes free or expired ones while it has
	// fewer. The collector manager calls it on every sync.
	Assign(ctx context.Context, wanted []int64) (map[int64]bool, error)
	// Singleton runs job while this server holds its lease and stops it when
	// the lease is lost, until ctx ends. Only one server runs a job at a time.
	Singleton(ctx context.Context, job string, run func(context.Context))
}

var _ Node = (*node)(nil)

// node is this server's membership.
type node struct {
	store   Repositories
	log     *slog.Logger
	metrics *obs.Metrics
	self    repository.Server
	mu      sync.RWMutex
	servers []repository.Server
	alive   int
	// collectors is the number of live servers with the collector role (at
	// least 1): targets are shared among them only.
	collectors int
	noJobs     bool
	assigned   map[int64]bool
	// freeFor counts the consecutive syncs a target was seen without a live
	// lease (orphan rescue).
	freeFor map[int64]int
}

// Self describes this server.
type Self struct {
	ID       string
	Hostname string
	Version  string
	// Roles are this server's roles; empty means every role.
	Roles []string
}

// orphanSyncs is how many syncs a target may stay without a collector before
// a server takes it beyond its share (for example while older servers, which
// do not know roles, share the targets among too many servers).
const orphanSyncs = 2

// New returns the node of a server (its ID is unique per running server).
// metrics may be nil.
func New(store Repositories, self Self, log *slog.Logger, metrics *obs.Metrics) Node {
	if metrics == nil {
		metrics = obs.NewMetrics()
	}
	roles := self.Roles
	if len(roles) == 0 {
		roles = repository.AllRoles
	}
	return &node{store: store, log: log, metrics: metrics, alive: 1, collectors: 1, assigned: map[int64]bool{}, freeFor: map[int64]int{},
		self: repository.Server{ID: self.ID, Hostname: self.Hostname, Version: self.Version, StartedAt: time.Now().UTC(), Roles: roles}}
}

// ID is the server's ID.
func (n *node) ID() string { return n.self.ID }

// Alone reports whether no other live server shares the store.
func (n *node) Alone() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.alive <= 1
}

// Servers returns the servers seen at the last heartbeat.
func (n *node) Servers() []repository.Server {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return slices.Clone(n.servers)
}

// Owns reports whether this server collects a target.
func (n *node) Owns(targetID int64) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.assigned[targetID]
}

// Run renews the heartbeat until ctx ends, then releases this server's
// target leases so the others take them over at once.
func (n *node) Run(ctx context.Context) {
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

func (n *node) beat(ctx context.Context) {
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
	byRole := map[string]int{}
	for _, s := range servers {
		if !s.Alive {
			continue
		}
		alive++
		for _, r := range s.Roles {
			byRole[r]++
		}
	}
	for _, r := range repository.AllRoles {
		n.metrics.ClusterServers.Set(float64(byRole[r]), r)
	}
	noJobs := byRole[repository.RoleJobs] == 0
	n.mu.Lock()
	n.servers, n.alive = servers, max(alive, 1)
	n.collectors = max(byRole[repository.RoleCollector], 1)
	was := n.noJobs
	n.noJobs = noJobs
	n.mu.Unlock()
	if noJobs {
		n.metrics.JobsMissing.Set(1)
		if !was {
			n.log.Warn("cluster: no live server has the jobs role; retention, rollups, baselines, the optimizer and notification reminders are not running")
		}
	} else {
		n.metrics.JobsMissing.Set(0)
		if was {
			n.log.Info("cluster: a server with the jobs role is back")
		}
	}
}

func (n *node) releaseAll() {
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
//
// It costs three statements whatever the number of targets: one to read the
// leases, one to take and renew (HoldLeases), one to release.
func (n *node) Assign(ctx context.Context, wanted []int64) (map[int64]bool, error) {
	leases, err := n.store.Leases(ctx, targetPrefix)
	if err != nil {
		return nil, err
	}
	n.mu.RLock()
	collectors := n.collectors
	n.mu.RUnlock()
	share := (len(wanted) + collectors - 1) / collectors
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

	keep := mine[:min(len(mine), share)]
	release := slices.Clone(mine[len(keep):]) // rebalance: another server takes them
	take := slices.Clone(free[:min(len(free), share-len(keep))])
	// Orphan rescue: a target no server took for more than orphanSyncs syncs
	// is taken even beyond the share.
	isFree := make(map[int64]bool, len(free))
	for _, id := range free {
		isFree[id] = true
	}
	n.mu.Lock()
	for id := range n.freeFor {
		if !isFree[id] {
			delete(n.freeFor, id)
		}
	}
	for _, id := range free[len(take):] {
		n.freeFor[id]++
		if n.freeFor[id] > orphanSyncs {
			take = append(take, id)
		}
	}
	for _, id := range take {
		delete(n.freeFor, id)
	}
	n.mu.Unlock()
	// Leases of targets that are no longer collected (disabled, deleted).
	for id, l := range held {
		if l.Holder == n.self.ID && !want[id] {
			release = append(release, id)
		}
	}

	names := make([]string, 0, len(keep)+len(take))
	for _, id := range keep {
		names = append(names, leaseName(id))
	}
	for _, id := range take {
		names = append(names, leaseName(id))
	}
	got, err := n.store.HoldLeases(ctx, names, n.self.ID, targetLeaseTTL)
	if err != nil {
		return nil, err
	}
	if len(release) > 0 {
		rel := make([]string, 0, len(release))
		for _, id := range release {
			rel = append(rel, leaseName(id))
		}
		if err := n.store.ReleaseLeases(ctx, rel, n.self.ID); err != nil {
			n.log.Warn("cluster: releasing leases failed; they expire on their own", "count", len(rel), "error", err.Error())
		}
	}
	owned := make(map[int64]bool, len(got))
	for _, name := range got {
		if id, ok := leaseTarget(name); ok {
			owned[id] = true
		}
	}
	n.mu.Lock()
	prev := n.assigned
	n.assigned = owned
	n.mu.Unlock()
	released := map[int64]bool{}
	for _, id := range release {
		released[id] = true
	}
	for id := range owned {
		if !prev[id] {
			n.metrics.LeaseChanges.Inc("taken")
		}
	}
	for id := range prev {
		switch {
		case owned[id]:
		case released[id]:
			n.metrics.LeaseChanges.Inc("released")
		default:
			n.metrics.LeaseChanges.Inc("lost")
		}
	}
	return owned, nil
}

// spread orders targets differently per server.
func (n *node) spread(id int64) uint32 {
	h := fnv.New32a()
	h.Write([]byte(n.self.ID + "/" + strconv.FormatInt(id, 10)))
	return h.Sum32()
}

// Singleton runs job while this server holds its lease and stops it when
// the lease is lost, until ctx ends. Only one server runs a job at a time.
func (n *node) Singleton(ctx context.Context, job string, run func(context.Context)) {
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
