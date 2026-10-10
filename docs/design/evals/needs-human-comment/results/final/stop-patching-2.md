## In plain English

I'm stopping work on ENG-301 because the design rests on something the Linear client doesn't do. The design says retry lives in one place, the client, and that works only if the client returns typed errors a caller can test for. It returns plain strings. So each caller has to know how the client words its rate-limit message, and I've now copied that check into three unrelated places.

Here is what the same check looks like in each caller today:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client rewords that message, all three callers stop retrying and nothing fails loudly. They just hit the rate limit and give up.

I think the fix is to make the client return a typed rate-limit error and put the retry inside it. That changes the client's public contract, which is your call, so I'm asking before I build it.

**Decision for you**
- Should the client return typed errors (for example a `RateLimitError` carrying the retry-after time) and own the retry? This is what I recommend. Callers then never inspect error text.
- Or do you want the string checks kept in the callers? I'd advise against it, since a message rewording silently breaks retry.

## Details

**State**
- Branch `thrawn01/eng-301-...` has three days of work. Nothing is merged and no PR is open.
- The same `strings.Contains(err.Error(), "rate limited")` plus `time.Sleep(backoff)` block is in the nudge sender, the status mover and the comment poster.
- No caller shares backoff state. Each one sleeps on its own, so concurrent calls can all retry at once and hit the limit again together.

**Why this is a design problem and not a coding one**
- The design assumed typed errors from the client. The client has none.
- The same workaround has turned up in three unrelated callers, and each one has to know the client's message wording.
- A fourth caller would need a fourth copy, so patching further makes this worse.

**What I'm doing**
- I'm stopping patching and moving ENG-301 to Needs Human.
- I will not revert anything, and I'll leave the branch as it is for you to look at.
- Once you answer, I'll replace the three string checks with one retry in the client behind a typed error, and add surface tests that call the client against a fake server returning a rate-limit response.

Tell me which way to go on the client's errors and I'll pick it back up from there.
