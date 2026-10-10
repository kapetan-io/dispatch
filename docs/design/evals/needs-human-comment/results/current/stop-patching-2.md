I've stopped work on ENG-301 and moved it to Needs Human. The design is wrong, and I'd rather you decide the fix than have me keep patching around it.

The design puts the rate-limit retry in the Linear client. It assumes the client returns typed errors (a dedicated error type that callers can check with `errors.As`), so code above the client can tell a rate limit from any other failure. The client doesn't do that. It returns plain strings, so the only way to spot a rate limit is to match the client's wording.

Over three days I've added the same check to three unrelated callers: the nudge sender, the status mover and the comment poster.

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

Each caller now depends on how the client words its messages. Rewording that one message turns off retry in all three places, and nothing flags it. The retry also lives in three copies instead of one, each with its own sleep.

I think the client should return a typed rate-limit error and do the retry itself, so callers only see the error when retries run out. I'd then delete the three string checks. This changes the client's public error contract, which makes it your call.

Reply here to approve that change to the design, or tell me what you'd rather do, and I'll pick the work back up.
