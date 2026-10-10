## In plain English

ENG-301 was supposed to make Linear calls retry when Linear says we're sending too many requests. The design put that retry in one place, the Linear client, and assumed the client reports failures as typed errors that callers can test for. It reports them as plain text, so the retry can't live in one place. I'm stopping and moving this to Needs Human.

Today a rate-limited call comes back as an error whose only signal is its wording. To retry, the nudge sender, the status mover and the comment poster each had to read that wording themselves:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client rewords that message, all three callers quietly stop retrying and nothing fails loudly. Any new caller has to copy the same check and the same backoff.

## Details

**What the design assumed.** The client returns typed errors, so a rate limit can be recognized without looking at text. It doesn't.

**What I found after three days.** I added the same special case in three unrelated callers (the nudge sender, the status mover, the comment poster). That matches the "same workaround in unrelated places" pattern, and callers now depend on how the client words its messages. I'm treating the design as wrong rather than patching a fourth caller.

**State.** Nothing is merged and no PR is open. The branch holds the three caller-side checks, which I won't ship as they are.

**What I recommend.** Change the client to return a typed rate-limit error and do the retry and backoff inside the client. Then delete the three caller checks. Callers would see a retried call succeed, or a final failure after retries run out.

**Decision for you.** Changing what the client returns changes its public contract and departs from the design, so it's your call. Do you want typed errors in the client (my recommendation), or do you want to keep the client as it is and rescope the ticket?

Once you answer, I'll rebuild on that basis.
