Write the explainer section for a database internals guide, for a developer who has not seen how databases survive a crash. The question it answers: How does a write-ahead log keep a database from losing committed data in a crash?

Facts, each checked against the Postgres documentation:

- A write-ahead log is a file where a database records each change before it makes the change.
- The database changes data pages in memory and writes them to disk later.
- Before it changes a page, it appends a record describing the change to the log.
- A transaction commits when its records and a commit record have been flushed to disk with fsync. The data pages do not have to be.
- Appending to the end of a file is sequential, so flushing the log costs much less than flushing pages scattered across the data files.
- After a crash the database replays the log from the last checkpoint and reapplies each record to its page.
- Every record has a log sequence number (LSN), and every page stores the LSN of the last record applied to it, so replay skips records a page already has.
- A checkpoint writes all modified pages to disk and notes the log position. The log before it can be deleted or reused.
- Postgres calls this the WAL, and replicas stay current by replaying the same records.
- Postgres stores the log in 16 MB segment files by default.
- In Postgres an LSN is a byte position in the log.

Write the final section to section.md.
