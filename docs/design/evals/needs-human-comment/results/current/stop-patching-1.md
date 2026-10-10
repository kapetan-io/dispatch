Stopping on ENG-301 and moving it to Needs Human. The design is wrong, and I'd rather say so now than ship a fourth workaround.

The design puts the rate-limit retry in the Linear client, on the assumption that the client returns typed errors. It returns plain strings. So every caller that wants a retry has to recognize the failure by its wording, and three of them now do:

- the nudge sender
- the status mover
- the comment poster

Each has this check:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

That is the same workaround in three unrelated places, and each copy depends on how the client words its messages. If someone rewords "rate limited", all three stop retrying and nothing fails loudly. Each copy also picks its own backoff, so the retry policy now lives in three places instead of the one the design promised.

I could finish by adding the check to the remaining callers, but that spreads the dependency further. I could also move the retry into the client, but that is not a small patch. It means giving the client typed errors, which changes the client's contract and touches every caller that handles its errors today. The design did not scope that, and the contract change is the user's call, not mine.

I think the design should add a typed rate-limit error to the client (for example a sentinel that callers match with `errors.Is`, carrying the retry-after delay Linear sends) and run the retry inside the client, so callers never see a rate limit at all. I have not built any of this.

My three-day branch has the string checks in the three callers and tests for them. Nothing is merged and there is no PR. I'll leave the branch as is so you can see the pattern.

Decision needed: do you want the design revised to add typed errors and a client-side retry, with this ticket redone against that, or do you want to keep the per-caller checks and accept the coupling?
