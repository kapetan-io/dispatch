#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so the cluster has no cache servers to run. Each process is told who its peers are through `SetPeers()`.

Keys sit on a consistent hash ring, and each key has one owner. A process finds the owner by hashing the key against its own copy of the ring, which needs no network call. When a peer joins or leaves, the keys on its part of the ring change owner and every other key keeps its owner.

`Get()` looks in two places on the local process before it goes anywhere else. The main cache holds the keys this process owns, and the hot cache holds copies of keys that peers own. On a miss in both, the process asks the owner. The owner runs the `Getter`, the function you write to load a value from its source, and passes the result back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space the process needs for its own keys.

Loads are deduplicated with singleflight, which allows one in-flight load per key. A million concurrent `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can end up running on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer. Delivery is best effort, and a peer that misses an update keeps its old copy, so values should carry an expiry.

The default cache evicts with an LRU, and the Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.
CANNED
