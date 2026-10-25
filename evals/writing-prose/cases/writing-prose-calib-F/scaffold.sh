#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers.

Peers are given to a process through `SetPeers()`. Keys sit on a consistent hash ring. A process finds a key's owner by hashing against its own copy of the ring, which takes no network call. Each key has one owner. When a peer joins or leaves, the keys on its part of the ring change owner and the others keep their owner.

A `Get()` checks the main cache, which holds the keys this process owns, and the hot cache, which holds copies of keys that peers own. If neither has the key, the process asks the owner. The owner runs the `Getter` and passes the value back through a `Sink`. The hot cache is bounded relative to the main cache. This keeps copies of peer-owned keys from taking over the keys the process owns.

Singleflight allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once.

If the owner is unreachable, the asking process runs the `Getter` itself. The `Getter` can therefore run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer, on a best-effort basis. Values should therefore carry an expiry.

The default cache is an LRU, and the Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport, and stats are OpenTelemetry metrics. There is no global state.
CANNED
