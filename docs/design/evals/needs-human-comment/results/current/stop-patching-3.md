Retry can't live in the Linear client the way the design says, so I've stopped work on ENG-301 and moved it to Needs Human.

The design assumes the client returns typed errors that callers can test for. It returns plain strings. Three days in, I've added the same check to three unrelated callers (the nudge sender, the status mover and the comment poster):

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

Each caller now has to know how the client words its messages. That's the pattern where I stop patching and treat the design as wrong, not the code.

**What this costs if I keep going**
- If anyone rewords the client's rate-limit message, retry quietly stops in all three callers and no test fails until a real rate limit hits.
- The substring match also fires on any error that happens to contain those words.
- Each caller owns its own `backoff`, so there are three retry policies where the design promised one.
- Every new Linear caller would need the same special case, and I'd expect the next one to forget it.

**What I recommend**
Give the client a typed rate-limit error (carrying the retry-after delay when Linear sends one) and move the retry loop inside the client. Then delete the three caller checks. Callers see a successful call or a final error and never handle rate limits.

This changes the shape of what the client returns, which is a public contract, so it's your call. A smaller version is to keep the client's error type as it is and only add a rate-limit sentinel error. I'd skip that because it still leaves the retry in the callers.

**What I need from you**
Approve the typed-error plus retry-in-client change, or tell me the design should go another way. Once you answer, I'll revise the design note, remove the three special cases and carry on from there.
