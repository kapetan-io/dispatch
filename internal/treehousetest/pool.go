// Package treehousetest is an in-process stand-in for treehouse that tracks
// leases and refuses what the real treehouse refuses.
package treehousetest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// Pool is a treehouse.Pool backed by memory. Its refusals come only from lease
// state. Failures that don't come from lease state (a build error, a timeout)
// go through FailTrees, FailLease and DelayTrees, and never change what the pool
// accepts. Unlike treehouse, it never builds a tree on demand, so a test seeds
// the trees it wants to lease.
type Pool struct {
	journal *journal.Journal

	mu           sync.Mutex
	trees        map[string][]treehouse.Tree
	moves        []leaseMove
	treesFail    []error
	leaseFail    error
	leaseFails   []error
	leaseTakes   []error
	treesDelay   time.Duration
	leaseHold    time.Duration
	leaseQuorum  int
	quorumMet    bool
	leasing      map[string]int
	peakLeasing  map[string]int
	peakAll      int
	beforeReturn func(path string)
}

type leaseMove struct{ path, holder string }

// New returns an empty Pool that records its calls in j.
func New(j *journal.Journal) *Pool {
	return &Pool{journal: j, trees: map[string][]treehouse.Tree{}, leasing: map[string]int{}, peakLeasing: map[string]int{}}
}

// Seed puts trees in the pool of repoDir exactly as given, so a test can set up
// any lease state.
func (p *Pool) Seed(repoDir string, trees ...treehouse.Tree) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := p.poolKey(repoDir)
	p.trees[key] = append(p.trees[key], trees...)
}

// Clear empties the pool of repoDir.
func (p *Pool) Clear(repoDir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.trees, p.poolKey(repoDir))
}

// AddTree puts an available tree at path in the pool of repoDir.
func (p *Pool) AddTree(repoDir, path string) {
	p.Seed(repoDir, treehouse.Tree{Path: path, Status: "available"})
}

// Holder says who holds the lease on the tree at path, or "" when nobody does.
func (p *Pool) Holder(path string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t := p.find(path); t != nil {
		return t.LeaseHolder
	}
	return ""
}

// Status says the pool status of the tree at path, or "" for an unknown path.
func (p *Pool) Status(path string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t := p.find(path); t != nil {
		return t.Status
	}
	return ""
}

// MoveLease hands the lease on the tree at path to holder at once.
func (p *Pool) MoveLease(path, holder string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.moveLease(path, holder)
}

// MoveLeaseAfterRead hands the lease on the tree at path to holder right after
// the next Trees call returns, which is a lease that changes after dispatch
// looked.
func (p *Pool) MoveLeaseAfterRead(path, holder string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.moves = append(p.moves, leaseMove{path, holder})
}

// FailTrees makes the next Trees calls fail, one error each.
func (p *Pool) FailTrees(errs ...error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.treesFail = append(p.treesFail, errs...)
}

// FailLease makes every Lease call fail with err, as treehouse does when it
// can't build a tree. FailLease(nil) restores normal leasing.
func (p *Pool) FailLease(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leaseFail = err
}

// FailLeasesOnce makes the next len(errs) Lease calls fail, one error each, and later calls
// lease normally, as treehouse does when it fails for a moment and works on the next try.
func (p *Pool) FailLeasesOnce(errs ...error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leaseFails = append(p.leaseFails, errs...)
}

// FailLeasesAfterTaking makes the next len(errs) Lease calls take a tree, mark it leased and then
// fail, as treehouse does when it exits nonzero after it recorded the lease.
func (p *Pool) FailLeasesAfterTaking(errs ...error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leaseTakes = append(p.leaseTakes, errs...)
}

// DelayTrees makes the next Trees call take d.
func (p *Pool) DelayTrees(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.treesDelay = d
}

// HoldLeases makes every Lease call take d, long enough for another launch in
// the same repo to start meanwhile. Real treehouse fetches while it leases, and
// two fetches into one clone's refs collide.
func (p *Pool) HoldLeases(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leaseHold = d
}

// AwaitLeases makes every Lease call wait until n Lease calls are running at once
// in all pools together, so a test sees two launches overlap without guessing how
// long the second takes to arrive. A wait that outlasts ten seconds gives up.
func (p *Pool) AwaitLeases(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leaseQuorum = n
}

// PeakLeasing is the most Lease calls that were running at once in the pool of
// repoDir. Above 1 means two launches could have fetched into the clone together.
func (p *Pool) PeakLeasing(repoDir string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peakLeasing[p.poolKey(repoDir)]
}

// PeakLeasingAnywhere is the most Lease calls that were running at once in all
// pools together.
func (p *Pool) PeakLeasingAnywhere() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peakAll
}

// BeforeReturn runs f with the tree's path at the start of every Return call,
// so a test can look at the world at the moment dispatch returns a tree.
func (p *Pool) BeforeReturn(f func(path string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.beforeReturn = f
}

func (p *Pool) Trees(ctx context.Context, repoDir string) ([]treehouse.Tree, error) {
	p.journal.Record("treehouse", "trees", repoDir)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	delay := p.treesDelay
	p.treesDelay = 0
	p.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Real treehouse can't run in a directory that doesn't exist.
	if _, err := os.Stat(repoDir); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.treesFail) > 0 {
		err := p.treesFail[0]
		p.treesFail = p.treesFail[1:]
		return nil, err
	}
	trees := append([]treehouse.Tree{}, p.trees[p.poolKey(repoDir)]...)
	for i := range trees {
		trees[i].RepoDir = repoDir
	}
	for _, m := range p.moves {
		p.moveLease(m.path, m.holder)
	}
	p.moves = nil
	return trees, nil
}

func (p *Pool) Lease(ctx context.Context, repoDir, ticket string) (treehouse.Tree, error) {
	p.journal.Record("treehouse", "lease", repoDir, ticket)
	if err := ctx.Err(); err != nil {
		return treehouse.Tree{}, err
	}
	if _, err := os.Stat(repoDir); err != nil {
		return treehouse.Tree{}, err
	}
	p.mu.Lock()
	key, hold := p.poolKey(repoDir), p.leaseHold
	p.leasing[key]++
	p.peakLeasing[key] = max(p.peakLeasing[key], p.leasing[key])
	total := 0
	for _, n := range p.leasing {
		total += n
	}
	p.peakAll = max(p.peakAll, total)
	p.mu.Unlock()
	time.Sleep(hold)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		p.mu.Lock()
		total := 0
		for _, n := range p.leasing {
			total += n
		}
		p.quorumMet = p.quorumMet || total >= p.leaseQuorum
		ready := p.quorumMet
		p.mu.Unlock()
		if ready {
			break
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leasing[key]--
	if len(p.leaseFails) > 0 {
		err := p.leaseFails[0]
		p.leaseFails = p.leaseFails[1:]
		return treehouse.Tree{}, err
	}
	if p.leaseFail != nil {
		return treehouse.Tree{}, p.leaseFail
	}
	trees := p.trees[p.poolKey(repoDir)]
	for i := range trees {
		if trees[i].Status == "available" {
			trees[i].Status, trees[i].LeaseHolder = "leased", ticket
			if len(p.leaseTakes) > 0 {
				err := p.leaseTakes[0]
				p.leaseTakes = p.leaseTakes[1:]
				return treehouse.Tree{}, err
			}
			leased := trees[i]
			leased.RepoDir = repoDir
			return leased, nil
		}
	}
	return treehouse.Tree{}, fmt.Errorf("all %d worktrees are in use or dirty", len(trees))
}

func (p *Pool) Return(ctx context.Context, repoDir, path, holder string) error {
	p.journal.Record("treehouse", "return", repoDir, path, holder)
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	hook := p.beforeReturn
	p.mu.Unlock()
	if hook != nil {
		hook(path)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	tree := p.find(path)
	switch {
	case tree == nil:
		return fmt.Errorf("worktree %s is not managed by treehouse", path)
	case tree.Status == "available":
		return fmt.Errorf("failed to return worktree: lease precondition failed: worktree %s is not leased", path)
	case tree.LeaseHolder != holder:
		return fmt.Errorf("failed to return worktree: lease precondition failed: lease holder does not match worktree %s", path)
	}
	tree.Status, tree.LeaseHolder = "available", ""
	return nil
}

// key names the pool a directory belongs to. Like treehouse, it finds the pool
// from the directory it runs in, which may be the clone or any tree of the
// pool, and a symlinked path and its target are one directory.
func (p *Pool) poolKey(dir string) string {
	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	for key, trees := range p.trees {
		for _, tree := range trees {
			if r, err := filepath.EvalSymlinks(tree.Path); (err == nil && r == resolved) || tree.Path == resolved {
				return key
			}
		}
	}
	return resolved
}

// resolve follows symlinks when the path exists, as treehouse sees it.
func resolve(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

func (p *Pool) find(path string) *treehouse.Tree {
	want := resolve(path)
	for _, trees := range p.trees {
		for i := range trees {
			if trees[i].Path == path || resolve(trees[i].Path) == want {
				return &trees[i]
			}
		}
	}
	return nil
}

func (p *Pool) moveLease(path, holder string) {
	if t := p.find(path); t != nil {
		t.Status, t.LeaseHolder = "leased", holder
	}
}

var _ treehouse.Pool = (*Pool)(nil)
