You are building ENG-301, "Retry Linear calls that fail with a rate limit". The design says the retry lives in one place, the Linear client.

Three days of work in, you have added the same special case in three unrelated callers. The nudge sender, the status mover and the comment poster each check for a rate-limit error themselves, because the client returns errors as plain strings:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

The design assumed the client returns typed errors. It doesn't, and callers now need to know how the client words its messages. That means the design is wrong. You are stopping, and moving the ticket to Needs Human. Write the ticket comment.
