## In plain English

Linear sometimes tells us to slow down, and ENG-301 is meant to make us wait and try again when that happens. The design put that retry in one place, the Linear client. It assumed the client would report a rate limit as a distinct kind of error. It doesn't. It returns every failure as a plain text message, so the only way to spot a rate limit is to read the words in the message.

So the nudge sender, the status mover and the comment poster each read the message themselves, and each has its own copy of this check:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If anyone rewords that message, all three callers quietly stop retrying and we are back to the failures this ticket exists to fix. Any new caller has to know the magic phrase too. The design is wrong, not the callers, so I have stopped patching and moved the ticket to Needs Human.

I recommend the client return a typed rate-limit error and do the retry itself. The three string checks then get deleted. That changes the design, so I want your go-ahead before I rebuild on it.

## Details

**State.** Three days in, the same special case sits in three unrelated callers. I have not opened a PR.

**Evidence.** The design says the retry lives in the Linear client and that callers never see rate limits. The client builds errors from strings, so there is nothing for a caller to test with `errors.As`. That is why the `strings.Contains` check showed up three times in code that otherwise has nothing in common. Callers now depend on the client's wording, which is the exact leak the design was meant to prevent.

**Decision for you.**

1. **Amend the design (my recommendation).** The client returns a typed error for rate limits, carrying the wait time Linear asks for if it sends one. The client retries inside itself. I remove the three caller checks. Callers see a rate limit only after retries run out, and it is one error type to handle.
2. **Keep the strings, but in one place.** The client matches its own message text and retries. This is a smaller change, but the client's errors stay untyped, and the next failure kind we want to handle has the same problem.

I'd go with 1. Reply on this ticket with 1 or 2, or tell me the design should change differently, and I'll pick it back up from there.
