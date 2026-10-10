## In plain English

I was adding retry for Linear rate-limit errors (Linear telling us to slow down), and the design said that retry would live in one place, the Linear client. The client reports failures as plain text instead of an error type, so the only way to spot a rate limit is to search the message for the words "rate limited". I've now copied that search into three unrelated callers (the code that sends nudges, the code that moves ticket status, and the code that posts comments), and each one breaks if the client rewords its message. The design assumed typed errors, so I've stopped and need you to decide how the client should report errors.

```go
// the same check now sits in all three callers
if strings.Contains(err.Error(), "rate limited") {
	time.Sleep(backoff)
}
```

## Details

**State.** Stopped three days in, ticket moved to Needs Human. I have not opened a PR.

**Evidence the design is wrong.**
- The design says retry lives in the client and callers stay unaware of it. That only works if the client returns errors a caller can tell apart without reading the message.
- The client returns plain strings, so the nudge sender, the status mover and the comment poster each match on the text themselves.
- Each caller now has its own backoff, so retry behaviour lives in three places and can drift.
- A caller breaks silently if someone rewords a client error. Nothing at compile time or in tests ties the two together.

**Attempts.** I patched the check into the first caller, then the second, then the third. That is the repeated workaround the build skill says to treat as a design problem, so I stopped there.

**Decision needed.** The shape of the client's error return is a public contract, so I'm asking you instead of choosing. I recommend a typed rate-limit error that callers can detect with `errors.As`, with retry and backoff moving inside the client. The three string checks would then be deleted, not patched. The alternative is to keep strings and accept retry in every caller, which I think defeats the point of ENG-301.

If you approve the typed error, tell me what the type should carry (for example, the retry-after delay Linear sends), and I'll rebuild on that.
