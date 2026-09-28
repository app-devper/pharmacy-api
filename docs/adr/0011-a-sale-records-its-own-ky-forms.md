# A sale records its own KY forms

Extends the `sales` module ([ADR-0005](./0005-commercial-commands-run-once-in-the-sales-module.md)).

Web and KMP each decided which KY forms (ขย.10/11/12) a bill needed and computed the regulated values themselves, then posted each form on its own after the bill. The two disagreed (ขย.12 value: base retail price on web, tier and unit price on KMP), and a form re-posted after a lost response was recorded twice, since the form endpoints have no request identity.

Now `POST /sales` takes what the cashier captured, `ky: {ky10, ky11, ky12}` (buyer, address, prescription, doctor, hospital, purpose, pharmacist). `sales` decides which lines need which form from each drug's report types and takes the rest from the recorded sale: the drug and registration number, the quantity and unit sold, what the customer paid for the line (bill discount spread as in [ADR-0008](./0008-one-rule-for-a-periods-sales.md)), the business day, and for ขย.10 the drug's stock after the sale. The forms are written in the sale's transaction, so the sale and its forms are recorded together and once per client request id. A capture missing a required field refuses the whole sale. Empty buyer address and pharmacist take the shop settings.

Each sale records `ky_status`: `none`, `recorded`, `skipped_cashier` (`ky_skipped_by_cashier`), `skipped_setting` (the shop's `ky.skip_auto`, decided by the server), or `separate` for a client that still posts forms to `/ky10`–`/ky12`. Those endpoints stay for queued sales from older clients and for manual entries; `separate` is not refused until those clients are gone.
