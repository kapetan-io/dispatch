## In plain English

The design for this ticket says Linear calls that hit a rate limit (Linear telling us we are sending too many requests) get retried in one place, the Linear client. That only works if the client reports failures as typed errors, meaning error values a caller can check by kind. It doesn't, it returns plain text. So three days in, the nudge sender, the status mover and the comment poster each read the error message and decide for themselves whether to wait and retry. I'm stopping because callers now depend on the client's exact wording, which means the design is wrong, and you need to decide how the client reports errors.

Here is the line I have pasted into all three callers.

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If anyone rewords that message in the client, all three callers stop retrying and nothing fails loudly.

## Details

**State**
- Ticket moved to Needs Human.
- The three copies are on the ticket's branch. Nothing is merged and no PR is open.
- I'm adding no more call sites.

**Evidence**
- The design assumes the client returns typed errors, and it doesn't.
- The same string check has appeared in three unrelated callers.
- Each caller now knows how the client words its messages, which the design said callers would never need.

**What I tried**
- Matching on the message text in each caller. It works today and breaks on any rewording.
- I did not try a shared string-matching helper. It would only move the coupling to a new place without removing it.

**Decision needed**

I think the client should return a typed rate-limit error and do the retry itself, so the callers lose their checks. This changes the shape of the client's public error contract, which is why it is your call and not mine.

- If you agree, I'll make that change in the client, delete the three caller checks and add surface tests that show a rate-limited call retrying without any caller involvement.
- If you want a different shape, such as callers handling the retry, tell me which and I'll update the design before touching code.

Do you want the typed error in the client?
