## In plain English

I stopped work on ENG-301 because the design and the code disagree. The design says retrying Linear rate-limit failures happens in one place, the Linear client, and it assumed the client reports failures as typed errors (a distinct error kind that callers can test for). The client returns plain strings instead, so three callers each had to read the message text to spot a rate limit. I need you to decide whether the client gets typed errors before I write any more of this.

```go
// the same check now sits in the nudge sender, the status mover and the comment poster
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**State**
- Three days in, the retry exists as three separate copies of the check above. None of them is in the client, where the design puts it.
- I'm not opening a PR from this branch as it stands, and I'm moving the ticket to Needs Human.

**Evidence**
- The nudge sender, the status mover and the comment poster are unrelated callers, and each needed the same workaround.
- Each caller depends on the exact wording "rate limited". If the client rewords that message, the retries stop silently and rate-limit failures surface as real errors.
- This matches the stop-patching pattern, so I stopped instead of adding a fourth copy.

**Attempts**
- I tried the string check in each caller because the client had no better signal. I haven't tried changing the client, because that changes its error contract and the contract is your call.

**Decision needed**
I recommend the client return a typed rate-limit error and run the backoff itself. The three callers then drop their checks and the design holds as written. The tradeoff is that every existing caller that inspects client errors sees a new error type, so please confirm the shape you want, for example an exported sentinel or error type that works with `errors.Is`.

The alternative is to amend the design so each caller owns its retry. I'd advise against it, since the next caller would copy the string check a fourth time.

Which way do you want it? I'll resume from your answer.
