## In plain English

ENG-301 asks me to retry Linear calls that fail with a rate limit, and the design puts that retry in one place, the Linear client. The design assumes the client returns typed errors, meaning errors a caller can test for by kind. It returns plain strings, so three unrelated callers (the nudge sender, the status mover and the comment poster) each read the message text to spot a rate limit. I have stopped because that copy-paste means the design is wrong, not the code. Fixing it means changing what the client returns, which is a contract change, so the call is yours.

This check now exists in all three callers:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client rewords that message, all three retries stop working and nothing fails loudly.

## Details

**State:** I stopped patching and moved the ticket to Needs Human. I have added no further callers.

**Evidence:**
- The same string match and sleep appear in three callers that share no code. Each one needs to know how the client words its rate-limit message.
- The design says the retry lives in the client. With string errors the client cannot tell callers what happened, so each caller works it out on its own.
- Each caller picks its own `backoff`, so the three can already disagree on how long to wait.

**Attempts:** I patched each caller as it hit the problem, which is how I got to three copies. I did not try to wrap the client's errors from outside, because that moves the string matching into a wrapper without removing it.

**Decision I need:** Should the client return a typed rate-limit error? I recommend it. The client would retry inside itself and surface the typed error only when retries run out. I would then delete all three caller checks. This is the design as written, plus the typed errors it assumed.

The cost is that every caller of the client sees a new error type, and any caller that matches on today's strings breaks. I would grep for those first.

Reply "go typed" and I will start on it. If you would rather keep string errors, tell me where the retry should live instead.
