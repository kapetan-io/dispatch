#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library that caches data across a cluster of processes. Each process holds a part of the cache and serves it to the others, so there are no cache servers to run. The application tells each process about the others by calling `SetPeers()`.

Every key has one owner. Peers sit on a consistent hash ring, and a process finds a key's owner by hashing the key against its own copy of the ring. When a peer joins or leaves, the keys on its part of the ring change owner and every other key stays where it is.

A `Get()` looks in two places on the local process first. The main cache holds the keys this process owns, and the hot cache holds copies of keys that peers own. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space needed for the keys the process owns. If both miss, the process asks the owner. The owner runs your `Getter`, the function that loads the value from wherever the data lives, and passes the result back through a `Sink`.

Loads for a key are deduplicated with singleflight, which allows one in-flight load per key. A million `Get("FOO")` calls run the `Getter` once, so a popular key that is missing does not send a burst of requests to your database.

Two behaviors matter when you decide whether groupcache fits. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process. And `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis. A peer that misses an update can keep serving the old value, so values should carry an expiry.

The default cache is an LRU. The Otter cache is available as an alternative and evicts with a lock-free S3-FIFO. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.
CANNED
