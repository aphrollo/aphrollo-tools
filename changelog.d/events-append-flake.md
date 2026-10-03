level: patch

The event log now numbers every record by the place it sits at in its month file, so two records of a repository never share a sequence number.

### What you will notice

- A writer that held the log's lock could read the file's size just before a writer that had given up on the lock appended, and number its record with a place the record was not at. The two records then read back with one sequence number, and `aphrollo why <seq>` could name the wrong one. A read now derives each number from the record's byte offset, which is the same number in every case where nothing wrote in between.
- Nothing about what is written changes: lines are still one write under the lock with the torn-line guard, and the store's records, which order by their own version, are unaffected.
