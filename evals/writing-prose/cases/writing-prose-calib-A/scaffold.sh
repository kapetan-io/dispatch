#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so the cache lives inside your own servers and there are no cache servers to run. You tell each process who its peers are by calling `SetPeers()`.

Keys sit on a consistent hash ring, and each key has one owner. A process finds a key's owner by hashing the key against its own copy of the ring. Adding or removing a peer moves the keys on that peer's part of the ring to new owners, and every other key keeps its owner.

`Get()` first checks two caches in the local process: the main cache, which holds the keys this process owns, and the hot cache, which holds copies of keys that peers own. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the keys the process owns. If both miss, the process asks the owner. The owner runs the `Getter`, the function you supply to load a value from its source, and passes the value back through a `Sink`.

Singleflight allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer, on a best-effort basis. A peer that does not receive one keeps its old value, so values should carry an expiry.

The default cache is an LRU. The Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.
CANNED
