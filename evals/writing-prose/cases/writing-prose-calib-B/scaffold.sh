#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library for caching across a cluster of processes. Each process holds part of the cache and serves it to the others, so the cluster has no cache servers to run. A process learns who its peers are through `SetPeers()`.

Keys are placed on a consistent hash ring, and every key has one owner. A process finds the owner of a key by hashing it against its own copy of the ring. When a peer joins or leaves, the keys on its part of the ring change owner and the rest of the ring is untouched.

A `Get()` looks in two local caches first. The main cache holds the keys the process owns, and the hot cache holds copies of keys that peers own. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space for the keys the process owns. If neither cache has the key, the process asks the owner. The owner runs the `Getter`, the function you supply to load a value, and passes the result back through a `Sink`.

Singleflight allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, which means the `Getter` can run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer. Delivery is best effort, so a peer can miss an update and keep serving the old value. Values should therefore carry an expiry.

The default cache is an LRU. The Otter cache is an alternative that uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.
CANNED
