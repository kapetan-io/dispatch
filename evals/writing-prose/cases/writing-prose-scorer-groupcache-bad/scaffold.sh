#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library that caches data across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to run. Each process is given its peers through `SetPeers()`.

Each key has exactly one owner. The keys sit on a consistent hash ring, and a process finds a key's owner by hashing against its own copy of the ring, with no network call. When a peer joins or leaves, only the keys on its part of the ring change owner. `Get()` looks in the process's main cache, which holds the keys it owns, and in its hot cache, which holds copies of keys that peers own. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the keys the process owns. On a miss it asks the owner, and the owner runs your `Getter` to load the value and passes it back through a `Sink`. Loads are collapsed per key through singleflight, so a million `Get("FOO")` calls run the `Getter` once.

If the owner is unreachable, the asking process runs the `Getter` itself, so the same load can run on more than one process. `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis, so values should carry an expiry rather than depend on a removal reaching everyone. [VERIFY: confirm that a removal is never retried]

The default cache is an LRU, and the Otter cache evicts with lock-free S3-FIFO, which avoids a lock on every hit. HTTP is the default transport, so peers need nothing beyond a port to reach each other. Stats are OpenTelemetry metrics, which any OpenTelemetry exporter can collect, and there is no global state, so two groups in one process do not interfere with each other. A process that wants more than one cache creates more than one group, and each group has its own Getter, its own main cache, and its own hot cache.
CANNED
