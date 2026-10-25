#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to run. Each process learns who its peers are through `SetPeers()`.

Keys sit on a consistent hash ring, and every process keeps its own copy of the ring. A process finds the owner of a key by hashing it against that copy, which gives each key exactly one owner. `Get()` first checks the main cache, which holds the keys the process owns, and the hot cache, which holds copies of keys that peers own. On a miss it asks the owner. The owner runs the `Getter` you supplied to load the value from its source, and passes the value back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space for the keys the process owns.

Concurrent requests for one key share a single load, because singleflight allows one in-flight load per key. A million `Get("FOO")` calls run the `Getter` once. Adding or removing a peer changes the owner of the keys on that peer's part of the ring, and every other key keeps its owner.

Two behaviours matter when deciding whether groupcache fits. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process at a time and has to tolerate that. And `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis, so a peer can miss an update. Values should carry an expiry so a stale copy ages out.

The default cache evicts in LRU order, and the Otter cache evicts with a lock-free S3-FIFO. Peers talk over HTTP by default, stats are reported as OpenTelemetry metrics, and groupcache keeps no global state.
CANNED
