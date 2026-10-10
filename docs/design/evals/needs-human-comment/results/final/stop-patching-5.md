## In plain English

The plan was for the Linear client to retry rate-limited calls itself, so no caller has to think about it. That can't work as designed, because the client reports every failure as a plain text message. The only way to spot a rate limit is to read the wording, so I ended up writing that check in each caller that needs it.

This is the check, and it now appears in the nudge sender, the status mover and the comment poster.

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client ever rewords that message, all three callers quietly stop retrying and nothing fails loudly. I stopped patching and moved this ticket to Needs Human, since a fourth copy would only deepen the problem.

I need you to decide whether the design may change so the client returns a typed error for a rate limit. I recommend that, and moving the retry into the client so callers delete their copies.

## Details

**State.** ENG-301 is in Needs Human. No more callers are being patched.

**Why the design is wrong.** It assumed the Linear client returns typed errors. It returns strings. Callers therefore have to know how the client words its messages, which is the sign of a wrong abstraction and not a missed edge case.

**Evidence.**
- The same `strings.Contains(err.Error(), "rate limited")` plus `time.Sleep(backoff)` workaround sits in three unrelated callers, the nudge sender, the status mover and the comment poster.
- Each copy picks its own backoff, so the retry behavior is already drifting.
- Three days of work went into this before the pattern was clear.

**Proposed change.**
- The client returns a typed rate-limit error, which callers can check with `errors.As`.
- The client does the retry, so the callers drop their sleeps and the string checks.
- I delete the three copies and cover the retry with surface tests on the client.

I think this fits the design's intent, since the retry was always meant to live in one place. The error type is a new shape for the client's public contract, which is why I'm asking before building it.

Reply here with approval or a different direction and I'll pick it back up.
