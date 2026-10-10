## In plain English

ENG-301 asks us to retry Linear calls that fail because Linear is rate limiting us. The design puts that retry in one place, the Linear client, which assumed the client reports failures as typed errors that callers can tell apart. It doesn't. It returns plain text messages, so I have been adding the same text check to every caller that hits a rate limit, and three of them (the nudge sender, the status mover and the comment poster) now carry it. The retry now depends on the client's wording, which is the opposite of what the design wanted, so I stopped patching and need you to decide how the client reports errors.

This is the check now copied into each of the three callers.

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

If the client rewords that message, all three callers stop retrying and nothing fails loudly.

## Details

**State**
- Three days in. I stopped after seeing the same special case in three unrelated callers. No further code changes until you answer.
- The ticket is moving to Needs Human.

**Why I think the design is wrong, not my code**
- Callers need to know how the client words its messages, so the client's contract leaks into its users.
- The same deviation showed up three times. A fourth Linear caller would need a fourth copy.
- The design's central claim, one retry in the client, cannot hold while errors are strings. The client has nothing to tell it a failure was a rate limit except matching its own text, and callers can't see anything else either.

**Decision needed**
Changing the client's error type changes a public contract, so it is yours to approve. I recommend adding a typed rate-limit error to the client and doing the retry inside the client, which is what the design intended. The three caller checks then go away and the callers never see a rate limit that the client could have retried. I'd keep the client's existing messages unchanged so nothing else that reads them breaks.

The alternative is to leave the client alone and keep the string checks, which I'd avoid for the reason above.

Do you approve the typed error and the retry moving into the client? If yes, I'll make that change and delete the three caller checks.
