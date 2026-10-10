Stopping ENG-301 and moving it to Needs Human. The design is wrong, and I'd rather say so now than ship a fourth copy of the workaround.

**What the design assumed**

The retry lives in one place, the Linear client, which returns typed errors. A rate limit would be one recognizable error type and the client would retry it.

**What the client does**

It returns plain strings. Nothing in the error says "rate limited" except its wording, so a caller can only detect one by matching text.

**Evidence**

After three days, the same check is in three unrelated callers, the nudge sender, the status mover and the comment poster:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

- Each caller sleeps on its own `backoff`, so the three can retry at once and hit the limit together.
- A reworded client message turns the retry off in all three with no compile error and no failing test.
- The retry the design put in one place is now spread over three callers, and each had to learn how the client words its messages.

That is the pattern the build skill says means the design is wrong. A fourth caller would add a fourth copy.

**State of the branch**

I have not opened a PR. The three string checks are on the branch and I have not merged them anywhere. I'd drop them whatever you decide.

**What I need from you**

I think the fix is to give the client typed errors first, a `RateLimitError` that carries the retry-after delay, and then put the retry inside the client as the design says. That changes the client's public contract, which makes it your call. Do you want the typed errors in this ticket, or split into a separate ticket that ENG-301 waits on?

I'll pick this up once you answer here.
