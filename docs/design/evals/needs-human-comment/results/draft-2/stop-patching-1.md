## In plain English

ENG-301 asks us to retry Linear calls that fail because Linear says we are sending too many requests. The design puts that retry in one place, the Linear client, and assumes the client reports a rate-limit failure as a recognizable error type. It doesn't. It returns plain text, so three unrelated parts of dispatch (the code that nudges workers, the code that moves ticket statuses, and the code that posts comments) each had to search the message text for "rate limited" to know when to wait. The design is wrong, so I stopped, and I need you to decide how the client should report errors.

The same check now appears in all three places:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**State:** moved to Needs Human. I'm making no further changes until you answer.

**Evidence the design is wrong**
- The same string match and sleep appears in three callers that share nothing else, which is the "same workaround in unrelated places" pattern.
- Each caller now depends on how the client words its messages. If the client rewords "rate limited", all three stop retrying, and nothing fails to tell us.
- The design's premise, that the client returns typed errors, is false for the current client. It was never checked against the code.

**What I tried**
- I added the check to the nudge sender first, then the status mover, then the comment poster. Each time I took the string match as a one-off. The third copy showed the pattern.

**Recommendation**
- Change the client to return a typed error for rate limits, carrying the retry delay Linear sends when it has one, and do the retry inside the client as the design intended.
- Delete the three caller checks.
- Callers would then see either success or a final failure and never handle rate limits themselves.

**Decision I need from you**
- This changes the shape of the client's public error contract, which is your call under the build rules. Do you want typed errors on the client (my recommendation), or a different approach, such as keeping plain-string errors and accepting the string match in one wrapper? If typed errors, should they cover only rate limits or all Linear failures?
