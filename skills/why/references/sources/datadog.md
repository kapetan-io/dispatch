# Datadog Telemetry

## What this source contains

The runtime record: what happened in production, not what was planned. Metrics, monitors, dashboards, APM traces, logs, incidents and notebooks. A metric or monitor existing means someone thought the number worth watching, and a monitor threshold is frequently the answer to "why is this clamped at N?".

## How to search it

Use the Datadog MCP, broad then narrow: owning services, then dashboards and monitors (note their queries and thresholds), then metrics, logs, spans and incidents around the target. Correlate a metric's trajectory with the add/change date ("`payment_timeout` spiked 2023-11-03, retry logic merged 2023-11-06").

Time-bound log and span queries (about 30 days either side of the change) and narrow by service and tag. Log volume is huge and unbounded searches time out; aggregate instead of dumping raw logs.

If the target looks defensive, search incidents around the time it was added. An incident timeline saying "added defensive check for X" is near-direct evidence.

## What good evidence looks like here

- A monitor whose query and threshold match the constraint the code enforces (code clamps to 100, monitor alerts when requests exceed 100/min)
- A dashboard created by the target's author, with widgets that correspond to what the code measures or guards against
- A metric showing a production spike immediately before the code was merged, and stable values after
- An incident record referencing the target code, the same symbols, or the same error strings
- Logs showing a specific error pattern the defensive code would prevent, timestamped in the window before the change

## Common pitfalls

- **Correlation is not causation.** A spike before a PR and stabilization after is suggestive, not definitive. Other changes may have landed in the same window. Check neighboring PRs.
- **Overfitting to the chart you found.** Datadog visualizations are *made* by humans and reflect that human's framing. A chart named "retry success rate" is evidence the team cared about retry success, not that it's why a specific line of code exists.
- **Vanished telemetry.** Metrics can be renamed, deleted, or have short retention. If you can't find data from the relevant window, that's a gap, not a null result.
- **Instrumented != caused.** A metric's existence tells you someone cared enough to measure something, not that the code was added *because* of it. Cross-reference with commit/PR dates.

## What to return

For each relevant item:
- Type (dashboard / monitor / metric / log pattern / trace / incident / notebook)
- Title or name
- Link or identifier (dashboard ID, monitor ID, metric name, incident ID)
- Owner/author and created/modified date
- The specific condition, query, or quote that bears on the question (verbatim where possible)
- Relevance: what this suggests about the target code, and how strong the connection is
