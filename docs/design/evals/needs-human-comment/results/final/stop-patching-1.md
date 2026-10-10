## In plain English

I was making Linear calls retry when Linear tells us we are sending requests too fast (a rate limit). The design said the Linear client would do the retrying in one place. The client returns its errors as plain text instead of a type callers can test for. So I added the same text check to three separate callers, and each of them now depends on the exact wording of the client's error messages. The design's assumption about the client is wrong, and how the client reports errors is a decision for you.

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**State.** Three days in, ENG-301 is stopping here and moving to Needs Human. I won't open a PR until this is settled.

**Evidence.** The identical check above now sits in three unrelated callers: the nudge sender, the status mover and the comment poster. If the client changes the wording of that message, all three stop retrying and nothing fails loudly. A fourth caller would need a fourth copy. The design assumed typed errors, and the client doesn't have them.

**Attempts.** I put the check in each caller because that was the only way to retry without changing the client's error contract. I have not touched that contract, because its shape is your call.

**Decision needed.** I recommend giving the client a typed rate-limit error and doing the retry and backoff inside the client. The three callers then lose their checks and never see rate limits unless retries run out. Callers that need the cause can use `errors.As`. This matches what the design intended. The alternative is to keep string matching and put it in a shared function, but callers would still depend on the client's wording and the design would stay wrong.

Do you approve the typed error on the client, and should I rework the three callers on this branch to match?
