# Product Analytics Warehouse (Databricks and similar)

The product and data view: what users did, which experiments ran, where a threshold constant came from. Datadog is the runtime view; this is the usage view.

## How to search it

Table names and event schemas differ per company, so discover them from the warehouse itself with `SHOW TABLES ... LIKE '*<keyword>*'` and `DESCRIBE TABLE` before querying. Reporting a result from a table you never confirmed exists is the classic failure. Prefer deduplicated, typed models over raw event tables when both exist. System tables (`system.query.history`) show who ran expensive queries.

Time-bound every query to a window around the ship date (about 30 days either side). These tables are huge and unbounded scans time out.

Patterns that pay off:

- Event volume before and after the PR merge. A step from zero to steady volume right after the merge suggests the PR launched the feature; a decay to zero suggests deprecation.
- Distribution (median, p99, max) of a property in the 14 days before the PR. A p99 matching the target's threshold constant suggests the number was chosen from data.
- Experiment exposure rows naming the target's flag key near the PR date.
- `system.query.history` filtered on the table or symbol, for the expensive queries that motivated a migration or rewrite.
- If the target reads a dbt model, the model's own git history carries the rationale. Hand that lead to the git investigator.

## Pitfalls

- **Instrumented is not caused.** An event existing means someone logged it, not that the target exists because of it. Pair it with a PR or commit citation before claiming causation.
- **Silent instrumentation changes.** A volume step may mean logging started, not that behavior changed. Check for instrumentation PRs in the same window.
- **Retention cliff.** A window older than the table's retention or the model's creation is a gap, not a null result. Say so, or the synthesizer reads "no rows" as "no activity".
- **Refresh lag and drift.** Modeled tables rebuild on a schedule, and a column today may not have existed when the target was written.
- **Notebooks are not queryable** through the SQL MCP. If the rationale may live in one, return a gap.

## What to return

Per finding: the fully-qualified table, the exact query, the time window, a compact numeric summary (not raw rows), and the date correlation with the ship date (for example "first row 2024-08-15, PR merged 2024-08-14"), marked direct, circumstantial or weak.
