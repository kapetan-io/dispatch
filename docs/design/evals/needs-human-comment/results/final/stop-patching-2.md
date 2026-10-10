## In plain English

I stopped building ENG-301 and moved it to Needs Human, because the design doesn't match the code. The design puts the retry for rate-limited Linear calls in one place, the Linear client, and assumes the client reports a rate limit as an error type callers can test for. It doesn't. The client returns plain-text errors, so the nudge sender, the status mover and the comment poster each search the message for the words "rate limited" and then sleep. If anyone rewords that message, all three stop retrying with no error to show for it.

This is the check I have copied into each of those three callers:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**Status:** Needs Human. Three days in, no PR open, nothing merged.

**Evidence:** The same string match and sleep now sits in three unrelated callers (nudge sender, status mover, comment poster). Each one depends on the client's wording, and each carries its own backoff. That matches two of the stop-patching signs in the build skill, the same workaround in unrelated places and callers needing to know an abstraction's internal rules.

**What I recommend:** Change the client to return a typed rate-limit error and put the retry and backoff inside the client, as the design intended. Then delete the three caller checks. Callers would never see a rate limit unless the retries run out.

**Decision I need from you:** That change alters the shape of the client's errors, which is a public contract, so I won't make it unasked. Do you approve the typed error with retry inside the client? If you'd rather keep the client's errors as they are, tell me where the retry should live instead.
