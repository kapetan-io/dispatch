## In plain English

The design for ENG-301 says the Linear client should retry rate-limited calls in one place. That only works if the client reports a rate limit as a recognizable error type, and it doesn't. It returns plain text, so three unrelated callers (the nudge sender, the status mover and the comment poster) each had to search the message for the words "rate limited" and sleep. I'm stopping because the design's premise is wrong, and fixing it changes how the client reports every error.

```go
// the same check now sits in all three callers
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**State**
- Three days in, I have the special case in three callers. I've stopped and added nothing further.
- The ticket is moving to Needs Human.

**Evidence**
- The client builds its errors as strings, so no caller can tell a rate limit from any other failure without matching on wording.
- Each caller now depends on the exact phrase "rate limited". If the client rewords that message, retries stop and nothing fails loudly.
- The same workaround appeared in three unrelated places, which is the sign the design is wrong and a patch won't fix it.

**What I tried**
- I added the string check to each caller to keep the design's single backoff behavior working. That was the wrong call, and I'm not extending it to any other caller.

**Decision I need**
Should the client return a typed rate-limit error and run the retry itself?

I think yes. Callers then never see a rate limit unless retries run out, and I would delete the three string checks. The catch is that the client's error contract changes for every caller, which is your call under the public-contract rule.

If you'd rather keep errors as strings, tell me. Then the design has to say the retry lives in the callers, and I'll pull it into one shared wrapper instead of three copies.
