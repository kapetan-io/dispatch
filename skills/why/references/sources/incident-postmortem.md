# Incident & Postmortem Context

Not a separate source, a **cross-cutting angle**. Incidents often motivate defensive code ("we added this check after the X outage"), so if the target looks defensive (null checks, retry logic, timeout handling, rate limiting, feature flags), specifically hunt for incident history across every available source:

- **Notion**: search for postmortems mentioning the target file, feature, or error string
- **Linear**: look for tickets labeled `incident`, `sev-*`, `postmortem-action-item`, `reliability`
- **Slack**: search `#sev-*` and `#incident-*` channels around the dates the target code was added
- **Git**: commits with messages like "fix for incident", "add defensive check", "revert" followed by "re-apply with..." are strong signals
- **Datadog**: `search_datadog_incidents` for formal incident records with timelines, dashboards and monitors created as postmortem action items
- **Sentry**: issues whose first-seen/last-seen window aligns with the target's PR ship date, stack traces through the target
- **Analytics warehouse**: events that classify an error condition often spike during an incident, and a drop after the target PR ships is circumstantial support.

If you find an incident link, fetch the full postmortem. Postmortems typically have an "Action Items" section that ties directly to code changes. Evidence corroborated across several sources is especially strong. Skip this angle for code that doesn't look defensive.
