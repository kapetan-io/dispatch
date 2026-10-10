**Needs your call.** ENG-301 can't land as designed, because the design assumes the Linear client returns typed errors and it returns plain strings. I've stopped work and moved this to Needs Human.

In plain words, the design says the client retries rate-limited calls, so no caller has to care. The client can't tell a rate limit from any other failure without matching on message text. So the nudge sender, the status mover and the comment poster each ended up checking for the rate limit themselves. If the client ever rewords its message, all three stop retrying and nothing fails loudly.

Example, the same check now sits in all three callers.

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

**Evidence**
- I added this check in three unrelated places over three days, which is the pattern that says the design is wrong and my patching isn't.
- Each caller now depends on the client's wording, which is the knowledge the design meant to keep inside the client.
- Every new Linear caller would need a fourth copy, and a fixed `time.Sleep(backoff)` in each ignores any retry-after hint Linear sends.

**What I recommend**
Give the client a typed rate-limit error and do the retry inside the client, as the design intended. Then delete the three string checks. This changes the client's error contract, which is why I'm asking instead of doing it.

**What I need from you**
1. Approve the typed-error change to the client, or tell me a different shape you want.
2. Say whether the design doc should be amended to state that the client returns typed errors, since it currently assumes this.

I'm making no further changes and opening no PR until you answer. The three caller patches stay unmerged on the branch, and I'll remove them once the client does the retry.
