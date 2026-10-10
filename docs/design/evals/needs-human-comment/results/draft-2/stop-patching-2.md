## In plain English

ENG-301 is meant to retry Linear calls that hit a rate limit, and the design puts that retry in one place, the Linear client. The design assumed the client reports a rate limit as a typed error a caller can test for, but it returns a plain string. So I have been adding the same check to three unrelated callers, the nudge sender, the status mover and the comment poster. I'm stopping because this is a design problem, not an implementation detail, and the design needs your call before I go on.

Each caller now carries this, which depends on the exact wording of the client's error message:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**State.** Three days in. I've stopped and will not patch further. The ticket is in Needs Human.

**Evidence the design is wrong.**
- The same workaround appears in three callers that share nothing else.
- Callers must know how the client words its errors. If the client rewords that message, the retries stop working and nothing fails loudly.
- Each caller picks its own backoff, so the retry policy the ticket wanted in one place is now spread over three.

**What I recommend.** Revise the design so the client returns a typed rate-limit error and runs the retry and backoff itself. Callers then drop their checks and never see a rate limit unless retries are exhausted. This also keeps the retry in the one place the design named.

**Decision I need from you.** Approve the revised design (typed errors in the client, retry owned by the client), or tell me a different direction. On approval I'll remove the three caller checks and rework the ticket on that basis.
