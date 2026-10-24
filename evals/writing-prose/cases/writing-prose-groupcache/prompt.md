Write the explainer section for the groupcache README, for a developer deciding whether to use groupcache. The question it answers: How does groupcache work, for someone deciding whether to use it?

Facts, each checked against the code:

- groupcache is a Go library for caching across a cluster of processes.
- Every process holds part of the cache and serves it to the others, so there are no cache servers.
- Peers are given to a process through `SetPeers()`.
- Keys sit on a consistent hash ring. A process finds a key's owner by hashing against its own copy of the ring, with no network call.
- Each key has one owner.
- `Get()` checks the main cache (keys the process owns) and the hot cache (copies of keys peers own) first, then asks the owner.
- The owner runs the `Getter` and passes the value back through a `Sink`.
- The hot cache is bounded relative to the main cache, so copies of peer-owned keys cannot take over the keys the process owns.
- When a peer joins or leaves, only the keys on its part of the ring change owner.
- Singleflight allows one in-flight load per key. A million `Get("FOO")` calls run the `Getter` once.
- If the owner is unreachable, the asking process runs the `Getter` itself, so the `Getter` can run on more than one process.
- `Set()`, `Remove()` and `RemoveKeys()` go to the owner and then to every other peer, best effort, so values should carry an expiry.
- The default cache is an LRU. The Otter cache uses lock-free S3-FIFO eviction.
- HTTP is the default transport.
- Stats are OpenTelemetry metrics.
- There is no global state.

Write the final section to section.md.
