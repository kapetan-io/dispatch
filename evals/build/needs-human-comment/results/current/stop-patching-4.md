I stopped work on ENG-301 and moved it to Needs Human. The design puts the retry in one place, but the Linear client gives callers no reliable way to tell a rate limit from any other failure.

**In plain words.** The design assumed the client returns typed errors, meaning an error that code can check directly. It returns plain text instead, so each caller has to read the message to find out what went wrong. Any code that wants to retry on a rate limit ends up doing this:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

**Evidence.** Three days in, that check lives in three unrelated callers. They are the nudge sender, the status mover and the comment poster. Each one sleeps with its own backoff and each one depends on the client's exact wording. If someone rewords that message, all three stop retrying at once.

The three copies are the pattern the build skill says to stop on. Callers now need to know how the client words its messages, and a fourth caller would need a fourth copy. Pushing on would add more of the same.

**Recommendation.** Make the client return a typed rate-limit error that callers check with `errors.As`. Then move the retry and backoff into the client, as the design intended, and delete the three string checks. This changes the client's public error contract, so I'm asking before I make it.

**Decision for you.** Do you approve the typed error and the retry inside the client? If so, I'll amend the design and rebuild on it. If you'd rather keep string matching and accept three callers coupled to the wording, tell me and I'll finish that way.
