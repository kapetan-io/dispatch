## In plain English

This ticket is about retrying when Linear tells us to slow down (a "rate limit" response). The design said to do that once, inside the Linear client, but the client reports every failure as a plain text message instead of a labeled error kind. So the nudge sender, the status mover and the comment poster each ended up reading the message text to spot a rate limit and sleeping on their own. I've stopped because the design assumed something the client doesn't do, and fixing it means changing what the client hands back to every caller, which is your call.

This check now exists in all three callers:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client rewords that message, all three callers quietly stop retrying and nothing fails loudly.

## Details

**State:** Three days in. The retry works in the three callers above, each with its own copy of the string check and its own sleep.

**Evidence the design is wrong:**
- Three unrelated callers needed the same workaround, which is the pattern that says the design is at fault rather than the code.
- Each caller now depends on the exact wording of the client's error text, which is an internal detail of the client.
- Every new caller would need to copy the check, and the design says retry lives in one place.

**What I tried:** I added the check in the nudge sender, then the status mover, then the comment poster, expecting the third to be the last. Stopping at three, because a fourth caller would repeat it.

**Decision needed:** Should the client return a typed rate-limit error, so callers can detect it with `errors.As` and the client does the retrying itself? That changes the error contract for every Linear call, so I'm not making it alone.

I think yes. It matches what the design assumed, it deletes all three string checks and their sleeps, and the retry ends up in the one place the ticket asked for. The cost is touching every call site that handles client errors, which I can scope once you confirm.

If you'd rather keep the client's errors as strings, the three copies need to go behind one shared helper. I think that leaves the wording dependency in place and is the weaker choice.

Tell me which way you want it and I'll pick the ticket back up.
