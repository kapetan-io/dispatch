## In plain English

ENG-301 is meant to make our Linear calls retry when Linear replies "you're sending too many requests". The design puts that retry in one place, the Linear client, so nothing else has to think about it. The client returns its errors as plain text, not as a recognizable error type, so each caller has to read the wording to learn that it was a rate limit. After three days I have copied that text check into three unrelated callers (the nudge sender, the status mover and the comment poster). I'm stopping because that means the design is wrong, and I need you to decide how the client reports errors before I write more.

Each of the three callers now contains this:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If anyone rewords the client's message, all three retries silently stop working, and nothing fails to tell us.

## Details

**Why I think the design is wrong.** The design assumed the client returns typed errors. It doesn't. This matches the patterns that call for stopping: the same workaround in unrelated places, and callers that need to know the client's internal rules (here, its message wording). Each caller also carries its own `backoff` and sleep, so the retry policy is now spread across three places, not held in one.

**State.** The three string checks are on the branch. Nothing is merged. I haven't touched shipped code, and I won't revert anything.

**Decision I need.** Changing how the client reports errors changes a public contract, so I'm not making that call alone.

1. **Typed errors in the client (my recommendation).** The client returns a distinct rate-limit error that callers can test with `errors.Is`. The client also owns the retry and backoff, as the design intended. I would delete the three string checks and the three sleeps. This fits the design as written, and callers stop depending on message wording.
2. **Keep string matching.** I'd move the check into one helper next to the client. It is smaller, but the wording dependency stays and the client's retry still isn't in the client. I'd reject this.

Reply with 1 or 2 and I'll continue. With 1, I'll also need to know whether you want the design doc amended in this PR or in its own ticket.
