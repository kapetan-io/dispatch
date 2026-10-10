## In plain English

Linear sometimes refuses requests for a while when we send too many, and ENG-301 is meant to make dispatch wait and try again when that happens. The design put that waiting in one place, the code that talks to Linear. That code reports failures as plain text rather than a labeled error type. So the three parts that call it (the nudge sender, the status mover and the comment poster) each had to read the message wording to spot a rate limit. The design's assumption is false and I've stopped patching, because changing what errors the client returns is a decision about a shared contract, and that decision is yours.

Each of the three callers now carries this check:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client's wording ever changes, all three stop retrying and nothing fails loudly.

## Details

**State.** Three days in, the retry exists as three separate copies. The client has no retry of its own. I've added no fourth copy.

**Evidence.**
- The same string check and sleep sit in three unrelated callers.
- Each caller picks its own `backoff`, so the retry behavior is already three different behaviors.
- Callers depend on the client's message text, which is an internal rule of the client leaking out. This is the "callers need to know an abstraction's internal rules" pattern from the build skill.

**Recommendation.** Have the client return a typed rate-limit error and do the retry inside itself. Callers would test the error type with `errors.As` instead of reading text, and I'd delete the three string checks. Fixing the client is also the smaller diff, since it replaces three copies with one.

**Decision needed.** Do you approve changing the client's error contract this way? If you want a different shape, such as a sentinel error or a retry wrapper around the client, tell me which and I'll build that instead. I'm waiting in Needs Human until you answer.
