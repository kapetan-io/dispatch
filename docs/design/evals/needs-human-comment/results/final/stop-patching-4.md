## In plain English

I stopped work on ENG-301 and I need you to decide how the Linear client reports errors. The design says retries on rate limits live in one place, the client. That only works if callers can tell a rate-limit failure from any other failure without reading error text, and today they can't.

The client returns errors as plain strings, so each caller checks the wording itself. This is what I found in three unrelated places:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client rewords that message, all three retries stop firing and nothing fails loudly. The call just gets treated as an ordinary error. I think the design assumed the wrong thing, so adding a fourth copy of this check would make it worse. Patching further is the wrong move.

I recommend giving the client typed errors, so a caller can ask "was this a rate limit, and how long should I wait?" without parsing text. The retry loop then moves into the client and the three callers lose their special cases. That changes the shape of the client's public errors, which is your call and not mine.

## Details

**State**
- Three days in. Nothing is merged. No PR is open.
- The same string check is in the nudge sender, the status mover and the comment poster, each with its own `time.Sleep(backoff)`.
- The three backoff values were chosen separately and don't match.

**Why this is the design being wrong and not my code overreaching**
- The same workaround turned up in three unrelated callers.
- Each caller now depends on how the client words its messages.
- The design said the client returns typed errors. It doesn't, and nothing in the ticket covers changing that.

**Decision needed**
1. Add typed errors to the client (a rate-limit error that carries the retry delay), then do the retry inside the client. This is my recommendation.
2. Keep string errors and put the matching in the client alone. This stays in one place but keeps the fragile wording check.
3. Leave the three caller checks as they are and drop the single-place requirement from the design.

If you pick 1 or 2, I'll remove the three caller checks and rebase. Which one do you want?
