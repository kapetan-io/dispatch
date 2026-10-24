#!/bin/bash
set -e
cat > canned.md <<'CANNED'
A write-ahead log is a file where a database records each change before it makes the change, so that a crash cannot lose a transaction the database has already reported as committed. Postgres calls this the WAL, and replicas stay current by replaying the same records.

The database changes data pages in memory and writes them to disk later. Before it changes a page, it appends a record describing the change to the log. A transaction commits when its records and a commit record have been flushed to disk with fsync. The data pages do not have to be. Appending to the end of a file is sequential, so flushing the log costs much less than flushing pages scattered across the data files. [VERIFY: confirm that fsync is the default sync method]

After a crash, the database replays the log from the last checkpoint and reapplies each record to its page. Every record has a log sequence number, which in Postgres is a byte position in the log, and every page stores the number of the last record applied to it, so replay skips records a page already has. A checkpoint writes all modified pages to disk and notes the log position, which lets the log before it be deleted or reused. Postgres stores the log in 16 MB segment files by default, and a segment is recycled once a checkpoint has passed it. A checkpoint also takes time, because the modified pages are written gradually so that the disk is not saturated, and a crash during a checkpoint replays from the previous one, so the log kept on disk must reach back that far, and a database that checkpoints rarely keeps more of it.
CANNED
