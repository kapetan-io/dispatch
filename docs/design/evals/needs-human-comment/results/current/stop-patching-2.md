Stopping ENG-301 and moving it to Needs Human. The design is wrong, and I'd rather say so now than ship a fourth copy of the workaround.

**What the design assumed**

Retry lives in one place, the Linear client, which returns typed errors so callers can tell a rate limit from anything else. It doesn't. The client returns plain strings, so the only way to spot a rate limit is to match the message text.

**What I found after three days**

The nudge sender, the status mover and the comment poster each carry this same check:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

Three unrelated callers needed the same workaround, and each one now depends on how the client words its errors. If the client rewords that message, all three stop retrying and nothing fails loudly. That is the pattern from the build skill: callers have to know the abstraction's internal rules.

**Why I'm not patching further**

Centralizing the string match in the client would give one copy instead of three. But it would still make the client's wording the contract, and it would not match what the design says the client returns. Fixing that means changing the client's error type, which is a public contract and your call.

**What I did and did not do**

- Nothing is merged and no PR is open. The three call-site checks are on the branch only.
- I have not touched the client's error handling.

**What I need from you**

Pick one:

1. **Typed errors in the client (my recommendation).** Add a rate-limit error type with a retry-after value, and have the client do the backoff. The three callers then drop their string checks. This is what the design intended, and it is a larger change than ENG-301 describes.
2. **Keep strings and centralize.** The client retries internally on its own message match, and callers never see rate limits. It is smaller, but the contract stays fragile.

Either way the design doc needs an update. Tell me which option and I'll resume from the branch, removing the three checks first.
