Stopping on ENG-301 and moving it to Needs Human. The design is wrong, and I'd rather say so now than ship a third copy of the workaround.

**What the design assumed**

The retry lives in one place, the Linear client, which returns typed errors that callers can test. It doesn't. The client returns plain strings, so the only way to spot a rate limit is to read the wording of the message.

**What I found after three days**

The same check is now in three unrelated callers, the nudge sender, the status mover and the comment poster:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

Each caller carries its own backoff, and each depends on the exact text the client happens to use. If the client rewords that message, all three silently stop retrying and nothing fails loudly. Retry is now spread across the callers, which is the opposite of what the design set out to do. This is the "callers need to know an abstraction's internal rules" pattern, and I hit it three times, so patching a fourth caller would be the wrong move.

**What I did not do**

I have not merged or opened a PR. The three special cases sit in my working branch and I have not reverted them, because I'd like you to decide what happens to them.

**Decision for you**

I think the fix is to give the client a typed rate-limit error and move the retry inside the client, so callers never see a rate limit unless retries run out. That changes the client's public error contract, which is your call under the build rules, so I'm not making it myself.

Two questions.
1. Do you want the typed error and client-side retry as the new design? If yes, I'll update the design, drop the three caller checks and build it.
2. Should the client keep returning strings for other errors for now, or should typing every error be its own ticket? I'd split it out, since only the rate-limit case blocks ENG-301.

I'll resume when you answer here.
