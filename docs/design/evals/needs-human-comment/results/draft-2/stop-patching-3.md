## In plain English

Three days into ENG-301, the plan to retry rate-limited Linear calls in one place has fallen apart. The design assumed the Linear client returns errors that code can tell apart by type, but it returns plain text. Each caller now reads the message to learn it was rate limited, and I have copied that text check into three unrelated callers (the nudge sender, the status mover and the comment poster). If anyone rewords the client's message, all three retries stop working without any error.

The check as it now appears in each of the three callers:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**Status:** moved to Needs Human. I stopped patching and nothing from this work has shipped.

**What the design said:** the retry lives only in the Linear client, and callers never see a rate limit.

**What I found:** the client returns plain string errors, so the design's premise of typed errors is false. Callers have to know how the client words its messages. The same workaround has shown up in three unrelated places, which is the sign that the design is wrong and not that my code is overreaching.

**Recommendation:** change the client to return a typed rate-limit error, carrying the retry delay if Linear supplies one. Do the retry inside the client, then delete the three string checks. This is what the design intended, and it makes the string match unnecessary.

**Decision needed:** this changes the client's public error contract, so it is yours to call. Do you approve a typed rate-limit error in the client, or do you want a different shape?
