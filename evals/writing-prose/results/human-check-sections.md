# The ten groupcache sections the user rated

Question: How does groupcache work, for someone deciding whether to use it?

Rated blind, 1 to 5 (A was not rated). Source and rating per section are in `human-check.json`. Best: E.

## Section A (baseline run 2, rating None)

groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so the cache lives inside your own servers and there are no cache servers to run. You tell each process who its peers are by calling `SetPeers()`.

Keys sit on a consistent hash ring, and each key has one owner. A process finds a key's owner by hashing the key against its own copy of the ring. Adding or removing a peer moves the keys on that peer's part of the ring to new owners, and every other key keeps its owner.

`Get()` first checks two caches in the local process: the main cache, which holds the keys this process owns, and the hot cache, which holds copies of keys that peers own. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the keys the process owns. If both miss, the process asks the owner. The owner runs the `Getter`, the function you supply to load a value from its source, and passes the value back through a `Sink`.

Singleflight allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer, on a best-effort basis. A peer that does not receive one keeps its old value, so values should carry an expiry.

The default cache is an LRU. The Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.

## Section B (baseline run 1, rating 1)

groupcache is a Go library for caching across a cluster of processes. Each process holds part of the cache and serves it to the others, so the cluster has no cache servers to run. A process learns who its peers are through `SetPeers()`.

Keys are placed on a consistent hash ring, and every key has one owner. A process finds the owner of a key by hashing it against its own copy of the ring. When a peer joins or leaves, the keys on its part of the ring change owner and the rest of the ring is untouched.

A `Get()` looks in two local caches first. The main cache holds the keys the process owns, and the hot cache holds copies of keys that peers own. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space for the keys the process owns. If neither cache has the key, the process asks the owner. The owner runs the `Getter`, the function you supply to load a value, and passes the result back through a `Sink`.

Singleflight allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, which means the `Getter` can run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer. Delivery is best effort, so a peer can miss an update and keep serving the old value. Values should therefore carry an expiry.

The default cache is an LRU. The Otter cache is an alternative that uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.

## Section C (attempt 3 run 1, rating 1)

groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to run. Each process learns who its peers are through `SetPeers()`.

Keys sit on a consistent hash ring, and every process keeps its own copy of the ring. A process finds the owner of a key by hashing it against that copy, which gives each key exactly one owner. `Get()` first checks the main cache, which holds the keys the process owns, and the hot cache, which holds copies of keys that peers own. On a miss it asks the owner. The owner runs the `Getter` you supplied to load the value from its source, and passes the value back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space for the keys the process owns.

Concurrent requests for one key share a single load, because singleflight allows one in-flight load per key. A million `Get("FOO")` calls run the `Getter` once. Adding or removing a peer changes the owner of the keys on that peer's part of the ring, and every other key keeps its owner.

Two behaviours matter when deciding whether groupcache fits. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process at a time and has to tolerate that. And `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis, so a peer can miss an update. Values should carry an expiry so a stale copy ages out.

The default cache evicts in LRU order, and the Otter cache evicts with a lock-free S3-FIFO. Peers talk over HTTP by default, stats are reported as OpenTelemetry metrics, and groupcache keeps no global state.

## Section D (hand-written good, rating 2)

groupcache is a Go library that caches data across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to run and no global state to keep in sync. Each process is given its peers through `SetPeers()` and reports its stats as OpenTelemetry metrics.

Each key has exactly one owner. The keys sit on a consistent hash ring, and a process finds a key's owner by hashing against its own copy of the ring. `Get()` looks in the process's main cache, which holds the keys it owns, and in its hot cache, which holds copies of keys that peers own. The default cache is an LRU, and the Otter cache evicts with lock-free S3-FIFO. On a miss it asks the owner over HTTP, the default transport, and the owner runs your `Getter` to load the value. Loads are collapsed per key through singleflight, so a million `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, so the same load can run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis, so values should carry an expiry rather than depend on a removal reaching everyone.

## Section E (user draft, rating 4)

Groupcache is a Go library that caches data across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to run. Each process is given its peers through calls to `SetPeers()`.

When the user calls `Get(key)` it looks in the process's main cache, which holds the keys it owns, and in its hot cache, which holds copies of keys that peers own. If no key is found, the key is looked up in a consistent hash ring to find the key's owner, and a request to that owner for the key is sent. The owner runs a defined `Getter` to load the value. Loads are collapsed per key through singleflight, so a million `Get("FOO")` calls run the `Getter` once.

If the owner is unreachable, the asking process runs the `Getter` itself, so the same load can run on more than one process. `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis, so values should carry an expiry rather than depend on a removal reaching everyone.

The default cache is an LRU, and the Otter cache evicts with lock-free S3-FIFO. HTTP is the default transport, stats are OpenTelemetry metrics, and there is no global state.

## Section F (loop run 1, rating 3)

groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers.

Peers are given to a process through `SetPeers()`. Keys sit on a consistent hash ring. A process finds a key's owner by hashing against its own copy of the ring, which takes no network call. Each key has one owner. When a peer joins or leaves, the keys on its part of the ring change owner and the others keep their owner.

A `Get()` checks the main cache, which holds the keys this process owns, and the hot cache, which holds copies of keys that peers own. If neither has the key, the process asks the owner. The owner runs the `Getter` and passes the value back through a `Sink`. The hot cache is bounded relative to the main cache. This keeps copies of peer-owned keys from taking over the keys the process owns.

Singleflight allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once.

If the owner is unreachable, the asking process runs the `Getter` itself. The `Getter` can therefore run on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer, on a best-effort basis. Values should therefore carry an expiry.

The default cache is an LRU, and the Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport, and stats are OpenTelemetry metrics. There is no global state.

## Section G (attempt 1 run 1, rating 1)

groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so the cluster has no cache servers to run. Each process is told who its peers are through `SetPeers()`.

Keys sit on a consistent hash ring, and each key has one owner. A process finds the owner by hashing the key against its own copy of the ring, which needs no network call. When a peer joins or leaves, the keys on its part of the ring change owner and every other key keeps its owner.

`Get()` looks in two places on the local process before it goes anywhere else. The main cache holds the keys this process owns, and the hot cache holds copies of keys that peers own. On a miss in both, the process asks the owner. The owner runs the `Getter`, the function you write to load a value from its source, and passes the result back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space the process needs for its own keys.

Loads are deduplicated with singleflight, which allows one in-flight load per key. A million concurrent `Get("FOO")` calls run the `Getter` once. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can end up running on more than one process.

`Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer. Delivery is best effort, and a peer that misses an update keeps its old copy, so values should carry an expiry.

The default cache evicts with an LRU, and the Otter cache uses lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.

## Section H (attempt 2 run 1, rating 1)

groupcache is a Go library that caches data across a cluster of processes. Each process holds a part of the cache and serves it to the others, so there are no cache servers to run. The application tells each process about the others by calling `SetPeers()`.

Every key has one owner. Peers sit on a consistent hash ring, and a process finds a key's owner by hashing the key against its own copy of the ring. When a peer joins or leaves, the keys on its part of the ring change owner and every other key stays where it is.

A `Get()` looks in two places on the local process first. The main cache holds the keys this process owns, and the hot cache holds copies of keys that peers own. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space needed for the keys the process owns. If both miss, the process asks the owner. The owner runs your `Getter`, the function that loads the value from wherever the data lives, and passes the result back through a `Sink`.

Loads for a key are deduplicated with singleflight, which allows one in-flight load per key. A million `Get("FOO")` calls run the `Getter` once, so a popular key that is missing does not send a burst of requests to your database.

Two behaviors matter when you decide whether groupcache fits. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process. And `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis. A peer that misses an update can keep serving the old value, so values should carry an expiry.

The default cache is an LRU. The Otter cache is available as an alternative and evicts with a lock-free S3-FIFO. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.

## Section I (attempt 3 run 2, rating 1)

groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so a deployment has no cache servers to run. The application tells each process who its peers are by calling `SetPeers()`, and the processes handle the rest among themselves.

Keys are placed on a consistent hash ring, and each key has exactly one owner. A process hashes a key against its own copy of the ring to find that owner. `Get()` looks in two local caches first: the main cache, which holds the keys this process owns, and the hot cache, which holds copies of keys that peers own. On a miss in both, it asks the owner. The owner runs your `Getter`, the function that loads the value from its real source, and hands the result back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of popular peer-owned keys cannot crowd out the keys the process owns.

Loads are collapsed by singleflight, which allows one in-flight load per key, so a million `Get("FOO")` calls run the `Getter` once. Because ownership comes from the ring, a peer that joins or leaves moves the keys on its part of the ring to new owners, and the rest of the keys stay where they were.

Three behaviours matter when deciding whether groupcache fits. First, if the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process and must be safe to do so. Second, `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis. A peer that misses an update keeps its old copy, so values should carry an expiry. Third, the default cache evicts with an LRU, and the Otter cache is available for lock-free S3-FIFO eviction.

HTTP is the default transport between peers. Stats are reported as OpenTelemetry metrics, and the library keeps no global state.

## Section J (attempt 1 run 2, rating 1)

groupcache is a Go library for caching across a cluster of processes. Every process holds part of the cache and serves it to the others, so there are no cache servers to deploy or operate. A process learns who its peers are through `SetPeers()`.

Each key has one owner. Keys sit on a consistent hash ring, and every process keeps its own copy of the ring, so it finds a key's owner by hashing the key against that copy. When a peer joins or leaves, the keys on its part of the ring move to new owners and every other key keeps the owner it had.

A call to `Get()` first checks two caches in the local process. The main cache holds the keys this process owns, and the hot cache holds copies of keys that peers own. If neither has the key, the process asks the owner. The owner runs your `Getter`, the function that loads the value from its real source, and passes the result back through a `Sink`. The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the space needed for the keys the process owns.

Loads are deduplicated by singleflight, which allows one in-flight load per key. A million `Get("FOO")` calls run the `Getter` once, and the rest wait for that result. This protects the backing store from a burst of requests for a key that has just expired or never been loaded.

Two behaviors need planning around. If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process and must be safe to run that way. And `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer on a best-effort basis. A peer that misses the message keeps its old value, so values should carry an expiry.

The default cache is an LRU, and the Otter cache offers lock-free S3-FIFO eviction. HTTP is the default transport between peers, stats are reported as OpenTelemetry metrics, and the library keeps no global state.
