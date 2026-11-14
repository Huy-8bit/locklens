// Package cluster lets several LockLens servers share one monitoring store.
//
// Every server registers itself and renews a heartbeat. The collection of
// each instance is held by one server through a lease ("target:<id>"); the
// collectors split the instances by their measured collection cost and
// capacity (see plan), and a server that stops renewing its leases loses
// them, so the others take its instances over within about a minute. Each
// holding of a lease has an epoch; writes about a target are fenced by it
// (see Fence). Jobs that must run once per cluster (retention, rollups,
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
	// fenceMargin: a server stops writing for its targets this long before
	// their leases could expire, counted on its own monotonic clock from the
	// last successful renewal (not from the store's expires_at, which a
	// stalled server cannot read in time).
	fenceMargin = 10 * time.Second
	// weightEvery is how often the measured cost of the targets is averaged
	// and stored; weightAlpha the weight of the newest minute.
	weightEvery   = time.Minute
	weightAlpha   = 0.2
	jobLeaseTTL   = 30 * time.Second
	jobRenewEvery = 10 * time.Second
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
	// Assign decides which of the wanted targets this server collects (see
	// plan): it renews the leases the plan keeps here, releases the others,
	// and takes free ones the plan gives it. The collector manager calls it
	// on every sync.
	Assign(ctx context.Context, wanted []int64) (map[int64]bool, error)
	// Fence returns the fence to write about a target under; false when this
	// server does not hold the target's lease, or may have lost it: its last
	// renewal is older than the lease minus a margin, measured on this
	// server's monotonic clock (self-fencing; not while it is alone).
	Fence(targetID int64) (repository.Fence, bool)
	// Busy records collector time spent on a target (its cost).
	Busy(targetID int64, d time.Duration)
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
	// assigned maps the targets held to their lease epochs; renewed is when
	// the last renewal was sent (monotonic).
	assigned map[int64]int64
	renewed  time.Time
	// freeFor counts the consecutive syncs a target was seen without a live
	// lease (orphan rescue).
	freeFor map[int64]int

	// Collection cost: busy time per target since weighedAt, and the targets
	// held then.
	// now is the wall clock for planning (lease hold times); replaceable in
	// tests. Self-fencing uses the monotonic clock (time.Since).
	now       func() time.Time
	busyMu    sync.Mutex
	busy      map[int64]time.Duration
	weighedAt time.Time
	weighed   map[int64]bool
}

// Self describes this server.
type Self struct {
	ID       string
	Hostname string
	Version  string
	// Roles are this server's roles; empty means every role.
	Roles []string
	// Capacity is how many collections the server runs at once; its share of
	// the targets is proportional. MaxTargets limits its targets (0: none).
	Capacity   float64
	MaxTargets int
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
	sv := repository.Server{ID: self.ID, Hostname: self.Hostname, Version: self.Version, StartedAt: time.Now().UTC(), Roles: roles}
	if slices.Contains(roles, repository.RoleCollector) {
		c := max(self.Capacity, 1)
		sv.Capacity = &c
		zero := 0.0
		sv.Load = &zero
		if self.MaxTargets > 0 {
			mx := self.MaxTargets
			sv.MaxTargets = &mx
		}
	}
	return &node{store: store, log: log, metrics: metrics, alive: 1, collectors: 1, assigned: map[int64]int64{}, freeFor: map[int64]int{},
		self: sv, now: time.Now, busy: map[int64]time.Duration{}, weighedAt: time.Now(), weighed: map[int64]bool{}}
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
	_, ok := n.assigned[targetID]
	return ok
}

// Fence returns the fence to write about a target under; see Node.
func (n *node) Fence(targetID int64) (repository.Fence, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	epoch, ok := n.assigned[targetID]
	// A server alone keeps writing through a store outage (as before
	// clusters): nobody else can take its targets over; the store still
	// checks the fence.
	if !ok || (n.alive > 1 && time.Since(n.renewed) >= targetLeaseTTL-fenceMargin) {
		return repository.Fence{}, false
	}
	return repository.Fence{TargetID: targetID, Holder: n.self.ID, Epoch: epoch}, true
}

// Busy records collector time spent on a target.
func (n *node) Busy(targetID int64, d time.Duration) {
	n.busyMu.Lock()
	n.busy[targetID] += d
	n.busyMu.Unlock()
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
	n.mu.RLock()
	self := n.self
	n.mu.RUnlock()
	if err := n.store.Heartbeat(bctx, self); err != nil {
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
	n.assigned = map[int64]int64{}
	n.mu.Unlock()
	for id := range ids {
		_ = n.store.ReleaseLease(ctx, leaseName(id), n.self.ID)
	}
}

// Assign decides which of the wanted targets this server collects; see
// Node and plan. A target the plan gives to another collector but that stays
// free for more than orphanSyncs syncs is taken here anyway (orphan rescue:
// older servers, which do not plan, or a collector that stopped).
//
// It costs four statements whatever the number of targets (read the leases,
// read the weights, take and renew, release), plus one a minute to store the
// measured weights.
func (n *node) Assign(ctx context.Context, wanted []int64) (map[int64]bool, error) {
	leases, err := n.store.Leases(ctx, targetPrefix)
	if err != nil {
		return nil, err
	}
	weights, err := n.store.TargetWeights(ctx)
	if err != nil {
		return nil, err
	}
	n.mu.RLock()
	servers := n.servers
	n.mu.RUnlock()
	members := n.members(servers)
	isMember := map[string]bool{}
	for _, m := range members {
		isMember[m.ID] = true
	}

	held := map[int64]repository.Lease{}
	for _, l := range leases {
		if id, ok := leaseTarget(l.Name); ok {
			held[id] = l
		}
	}
	want := map[int64]bool{}
	current := map[int64]Holding{}
	var planFor, mine, free []int64
	for _, id := range wanted {
		want[id] = true
		l, ok := held[id]
		switch {
		case ok && l.Holder == n.self.ID:
			mine = append(mine, id)
			current[id] = Holding{Holder: l.Holder, Since: l.AcquiredAt}
			planFor = append(planFor, id)
		case ok && l.Live && isMember[l.Holder]:
			current[id] = Holding{Holder: l.Holder, Since: l.AcquiredAt}
			planFor = append(planFor, id)
		case ok && l.Live:
			// Held by a server that is not a live collector (its heartbeat
			// stopped): it can only be taken once the lease expires.
		default:
			free = append(free, id)
			planFor = append(planFor, id)
		}
	}
	w := map[int64]float64{}
	for id, tw := range weights {
		if v, ok := tw.Value(); ok {
			w[id] = v
		}
	}
	p := plan(planFor, current, w, members, n.now())

	var keep, take, release []int64
	for _, id := range mine {
		if p.owner[id] == n.self.ID {
			keep = append(keep, id)
		} else {
			release = append(release, id) // the plan moves it, or this server is over its limit
		}
	}
	slices.SortFunc(free, func(a, b int64) int { return cmp.Compare(n.spread(a), n.spread(b)) })
	isFree := make(map[int64]bool, len(free))
	n.mu.Lock()
	for _, id := range free {
		isFree[id] = true
		if p.owner[id] == n.self.ID {
			take = append(take, id)
			delete(n.freeFor, id)
			continue
		}
		n.freeFor[id]++
	}
	for id := range n.freeFor {
		if !isFree[id] {
			delete(n.freeFor, id)
		}
	}
	limit := 0
	if n.self.MaxTargets != nil {
		limit = *n.self.MaxTargets
	}
	for _, id := range free {
		if n.freeFor[id] > orphanSyncs && (limit <= 0 || len(keep)+len(take) < limit) {
			take = append(take, id)
			delete(n.freeFor, id)
		}
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
	sent := time.Now()
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
	owned := make(map[int64]int64, len(got))
	load := 0.0
	cost := costs(planFor, w)
	for name, epoch := range got {
		if id, ok := leaseTarget(name); ok {
			owned[id] = epoch
			load += cost[id]
		}
	}
	n.mu.Lock()
	prev := n.assigned
	n.assigned, n.renewed = owned, sent
	if n.self.Load != nil {
		n.self.Load = &load
	}
	n.mu.Unlock()
	n.metrics.CollectorLoad.Set(load)
	if c := n.self.Capacity; c != nil {
		n.metrics.CollectorShare.Set(p.share[n.self.ID])
	}
	n.metrics.Unassigned.Set(float64(len(planFor) - len(p.owner)))
	released := map[int64]bool{}
	for _, id := range release {
		released[id] = true
	}
	for id := range owned {
		if _, ok := prev[id]; !ok {
			n.metrics.LeaseChanges.Inc("taken")
		}
	}
	for id := range prev {
		_, still := owned[id]
		switch {
		case still:
		case released[id]:
			n.metrics.LeaseChanges.Inc("released")
		default:
			n.metrics.LeaseChanges.Inc("lost")
		}
	}
	n.weigh(ctx, owned, weights)
	out := make(map[int64]bool, len(owned))
	for id := range owned {
		out[id] = true
	}
	return out, nil
}

// members returns the live collectors to plan with, this server included.
// A collector that predates capacities counts with the average capacity.
func (n *node) members(servers []repository.Server) []Member {
	var out []Member
	var known float64
	nKnown := 0
	self := false
	for _, s := range servers {
		if !s.Alive || !s.Has(repository.RoleCollector) {
			continue
		}
		m := Member{ID: s.ID}
		if s.Capacity != nil && *s.Capacity > 0 {
			m.Capacity = *s.Capacity
			known += m.Capacity
			nKnown++
		}
		if s.MaxTargets != nil {
			m.MaxTargets = *s.MaxTargets
		}
		self = self || s.ID == n.self.ID
		out = append(out, m)
	}
	if !self && n.self.Capacity != nil {
		// Before this server's first heartbeat was read back.
		m := Member{ID: n.self.ID, Capacity: *n.self.Capacity}
		if n.self.MaxTargets != nil {
			m.MaxTargets = *n.self.MaxTargets
		}
		known += m.Capacity
		nKnown++
		out = append(out, m)
	}
	avg := 1.0
	if nKnown > 0 {
		avg = known / float64(nKnown)
	}
	for i := range out {
		if out[i].Capacity == 0 {
			out[i].Capacity = avg
		}
	}
	return out
}

// weigh averages the measured cost of the targets held for the whole last
// weightEvery and stores it, once per weightEvery.
func (n *node) weigh(ctx context.Context, owned map[int64]int64, stored map[int64]repository.TargetWeight) {
	n.busyMu.Lock()
	elapsed := time.Since(n.weighedAt)
	if elapsed < weightEvery {
		n.busyMu.Unlock()
		return
	}
	busy := n.busy
	n.busy = map[int64]time.Duration{}
	n.weighedAt = time.Now()
	prevOwned := n.weighed
	n.weighed = make(map[int64]bool, len(owned))
	for id := range owned {
		n.weighed[id] = true
	}
	n.busyMu.Unlock()

	var ids []int64
	var values []float64
	for id := range owned {
		if !prevOwned[id] {
			continue // not held for the whole window
		}
		rate := busy[id].Seconds() * float64(time.Minute) / float64(elapsed)
		v := rate
		if old := stored[id].BusyPerMin; old != nil {
			v = *old + weightAlpha*(rate-*old)
		}
		ids = append(ids, id)
		values = append(values, v)
	}
	if err := n.store.SaveTargetWeights(ctx, ids, values); err != nil {
		n.log.Warn("cluster: storing target weights failed", "error", err.Error())
	}
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
