Stopping ENG-301 and moving it to Needs Human, because the design is wrong and I'd be shipping a workaround instead of the feature.

The design puts rate-limit retry in the Linear client. That only works if the client returns typed errors, and it returns plain strings. So callers can't ask the client "was this a rate limit?" and the retry never landed in one place. Over three days I wrote the same check in three unrelated callers:

- the nudge sender
- the status mover
- the comment poster

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

Each caller now depends on how the client words its messages. If the client rewords that error, all three break silently, and they stop retrying without anything failing a test. Three callers with the same workaround is the pattern that says the design is wrong, not the code.

I have not merged or opened a PR. The three special cases are on the branch only. I'm leaving them there so you can see the shape of the problem, and I won't add a fourth.

**What I think the fix is.** The client should return a typed error, for example a `RateLimitError` that carries the retry-after time, and do the backoff itself. Callers then keep no retry logic at all. This reaches past ENG-301's scope, since it changes the client's public error contract, which is a call for you and not me.

**What I need from you.**

1. Do you want the typed-error change in the client folded into ENG-301, or split into its own ticket that ENG-301 depends on?
2. If it is split, should I discard the three caller checks on this branch, or keep them until the client change lands?

I'll rework the ticket once you pick.
