I've stopped work on ENG-301 and need a decision on how to proceed. The design says the retry lives in one place, the Linear client, and that plan doesn't hold up. The client returns errors as plain strings, and the design assumed typed errors.

Without a type to check, three unrelated callers (the nudge sender, the status mover and the comment poster) each test the message text themselves:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

That's the same workaround in three unrelated places, and each caller now depends on how the client words its messages. If the client rewords one, that caller silently stops retrying and nothing fails. Each copy also has its own backoff. I'm stopping here because patching a fourth caller would make this worse.

**Recommendation**

I'd change the client to return a typed rate-limit error. Callers would then stop checking message text, and the client would own the one retry loop the design describes. I'd delete the three special cases in the same change.

This changes what the client returns to every caller, so it reaches past the ticket's scope and I want your approval first. If you'd rather keep string errors and give the client a sentinel value to match, I can do that instead. It's a smaller change, but it still forces every caller to know about the retry.

**State of the branch**

- Nothing is merged and no PR is open. The three caller workarounds exist only on the branch.
- I haven't reverted anything, and I won't until you pick a direction.

I'll resume as soon as you reply here with the direction you want.
