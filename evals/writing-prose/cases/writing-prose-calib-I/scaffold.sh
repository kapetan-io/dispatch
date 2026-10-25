#!/bin/bash
set -e
cat > canned.md <<'CANNED'
groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so a deployment has no cache servers to run. The application tells each process who its peers are by calling `SetPeers()`, and the processes handle the rest among themselves.

Keys are placed on a consistent hash ring, and each key has exactly one owner. A process hashes a key against its own copy of the ring to find that owner. `Get()` looks in two local caches first: the main cache, which holds the keys this process owns, and the hot cache, which holds copies of keys that peers own. On a miss in both, it asks the owner. The owner runs your `Getter`, the function that loads the value from its real source, and hands the result back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of popular peer-owned keys cannot crowd out the keys the process owns.

Loads are collapsed by singleflight, which allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once. Because ownership comes from the ring, a peer that joins or leaves moves the keys on its part of the ring to new owners, and the rest of the keys stay where they were.

Three behaviours matter when deciding whether groupcache fits. First, if the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process and must be safe to do so. Second, `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis. A peer that misses an update keeps its old copy, so values should carry an expiry. Third, the default cache evicts with an LRU, and the Otter cache is available for lock-free S3-FIFO eviction.

HTTP is the default transport between peers. Stats are reported as OpenTelemetry metrics, and the library keeps no global state.
CANNED
