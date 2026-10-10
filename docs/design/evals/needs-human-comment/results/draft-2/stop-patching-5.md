## In plain English

Retrying Linear calls that hit a rate limit (Linear telling us to slow down) was meant to live in one place, the code that talks to Linear. That code returns errors as plain text, so three unrelated callers (the one that sends nudges to workers, the one that moves ticket statuses, and the one that posts comments) each read the text to decide whether to retry. Each caller now breaks if the wording changes, which is the opposite of what the design wanted. I stopped because the design assumed errors with a type, and fixing that changes what the Linear client promises to every caller, which is your call.

Each of the three callers now has this check:

```go
if strings.Contains(err.Error(), "rate limited") { time.Sleep(backoff) }
```

## Details

**State.** ENG-301 moves to Needs Human. I am not writing more retry code until the client's error contract is settled.

**Evidence.** The design says the client returns typed errors, and it returns `error` values built from strings. The same workaround appears in three callers that share nothing else. Each one needs to know the exact phrase "rate limited". That matches two of the signs for a wrong design, the same workaround in unrelated places and callers needing the internal rules of an abstraction.

**Attempts.** I added the string check to the nudge sender, the status mover and the comment poster over three days. I did not try to patch the client first, because that changes behavior for every existing caller.

**Decision needed.** How should the client report a rate limit? I recommend the client does the retrying itself with backoff, which is what the design promised, and returns a typed error (`ErrRateLimited`, checked with `errors.Is`) only when it gives up. Callers then carry no retry logic at all. I think this is right because it deletes the three special cases instead of making them sturdier. The other path is the typed error alone with callers still retrying, which keeps three copies of the backoff.

If you agree, I will remove the three string checks, put the retry and the typed error in the client, and update the design to say so. Which way do you want it?
