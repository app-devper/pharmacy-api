# End-of-day close snapshots the day

Implements [ADR-0003](./0003-durable-end-of-day-close.md) in the `sales` module ([ADR-0005](./0005-commercial-commands-run-once-in-the-sales-module.md)).

`POST /report/eod/close {date, closed_by_name}` (ADMIN+) closes one business day, a calendar date in the pharmacy's timezone: today or an earlier day, never a future one. The close stores the day's report as it stands (totals and bills), the verified user id, the name to print, and the close time. The business day is the command's identity: closing a day again returns the existing close with `Idempotent-Replayed: true`.

The snapshot never changes. A sale confirmed on a closed day, a void of a closed day's bill, or a return made on a closed day records a **Late sale adjustment** in the same transaction: its kind, reference, effect on bill count, total sales and net cash, user, and time. Days follow the live report's rules (a sale by confirmation time, a void by its bill's day, a return by the day it was made), so a closed day's snapshot plus its adjustments always equals the live report. `GET /report/eod` returns the live report for an open day and, for a closed day, the snapshot with `close` and `adjustments`.

Every command writes a guard document for the day it affects (`eod_days`), so a close and a sale of the same day cannot both commit without one seeing the other; MongoDB retries the loser. Without the guard, sales committing during a close were lost from both the snapshot and the adjustments.

Business days are still assigned by backend confirmation time; the cashier submission time of KMP ADR-0009/0012 is a separate change. Closes are per day, not per shift.
