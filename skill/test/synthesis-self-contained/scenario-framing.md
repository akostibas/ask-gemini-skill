I want a second opinion on a database migration strategy, but first let me give
you the constraints I'm operating under so you can frame your answer around them:

- **Constraint 1 — zero downtime.** The service is 24/7; I cannot take a
  maintenance window. Any migration has to be safe under concurrent reads and
  writes the entire time.
- **Constraint 2 — a 200M-row table.** Whatever we do has to complete in
  bounded time without holding a long lock or blowing out replication lag.
- **Constraint 3 — easy rollback.** If something looks wrong at 2am, on-call
  needs to be able to reverse course without a second migration.

The change itself: I need to split a `users.full_name` column into
`first_name` / `last_name`, backfill from the existing data, and eventually drop
the old column.

I'm trying to decide between the classic expand/contract (add new columns, dual-
write, backfill, switch reads, drop old) versus a shadow-table approach (build a
new table, backfill, swap).

Frame your recommendation explicitly around the three constraints I listed —
tell me which approach best satisfies each one and where the tension is. And ask
me any clarifying questions first if you need them.
