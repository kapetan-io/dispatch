#!/bin/bash
set -e
cat > canned.md <<'CANNED'
A write-ahead log is a file where a database records each change before it makes the change, so that a crash cannot lose a transaction the database has already reported as committed.

The database changes data pages in memory and writes them to disk later. Before it changes a page, it appends a record describing the change to the log. A transaction commits when its records and a commit record have been flushed to disk with fsync. The data pages do not have to be. Appending to the end of a file is sequential, so flushing the log costs much less than flushing pages scattered across the data files.

After a crash, the database replays the log from the last checkpoint and reapplies each record to its page. Every record has a log sequence number, and every page stores the number of the last record applied to it, so replay skips records a page already has. A checkpoint writes all modified pages to disk and notes the log position, which lets the log before it be deleted or reused.
CANNED
