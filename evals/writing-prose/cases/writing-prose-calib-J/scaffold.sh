#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to deploy or operate. A process learns who its peers are through `SetPeers()`.

Each key has one owner. Keys sit on a consistent hash ring, and every process keeps its own copy of the ring, so it finds a key's owner by hashing the key against that copy. When a peer joins or leaves, the keys on its part of the ring move to new owners and every other key keeps the owner it had.

A call to `Get()` first checks two caches in the local process. The main cache holds the keys this process owns, and the hot cache holds copies of keys that peers own. If neither has the key, the process asks the owner. The owner runs your `Getter`, the function that loads the value from its real source, and passes the result back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space needed for the keys the process owns.

Loads are deduplicated by singleflight, which allows one in-flight load per key. A million `Get("FOO")` calls run the `Getter` once, and the rest wait for that result. This protects the backing store from a burst of requests for a key that has just expired or never been loaded.

Two behaviors need planning around. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process and must be safe to run that way. And `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis. A peer that misses the message keeps its old value, so values should carry an expiry.

The default cache is an LRU, and the Otter cache offers lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.
CANNED
