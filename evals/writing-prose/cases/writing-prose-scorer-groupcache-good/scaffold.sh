#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library that caches data across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to run and no global state to keep in sync. Each process is given its peers through `SetPeers()` and reports its stats as OpenTelemetry metrics.

Each key has exactly one owner. The keys sit on a consistent hash ring, and a process finds a key's owner by hashing against its own copy of the ring. `Get()` looks in the process's main cache, which holds the keys it owns, and in its hot cache, which holds copies of keys that peers own. The default cache is an LRU, and the Otter cache evicts with lock-free S3-FIFO. On a miss it asks the owner over HTTP, the default transport, and the owner runs your `Getter` to load the value. Loads are collapsed per key through singleflight, so a million `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, so the same load can run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis, so values should carry an expiry rather than depend on a removal reaching everyone.
CANNED
