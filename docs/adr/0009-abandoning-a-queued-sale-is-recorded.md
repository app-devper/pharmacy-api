# Abandoning a queued sale is recorded

Implements the server side of KMP [ADR-0010](../../../pharmacy-app-kmp/docs/adr/0010-resolve-offline-sale-conflicts-explicitly.md) in the `sales` module ([ADR-0005](./0005-commercial-commands-run-once-in-the-sales-module.md)).

A client whose queued sale is refused (400, 409, 422) stops retrying it and keeps its payload. When the pharmacy decides the sale will never be recorded, an ADMIN+ user abandons it with `POST /sales/abandon {client_request_id, kind: "sale", payload, reason}`. The abandonment keeps the payload as queued, the reason, the verified user, and the time. Afterwards `POST /sales` with that request id is refused with 409, so a stale copy of the queue on another device cannot record it later. If the sale was already recorded, abandoning is refused with 409 and the recorded sale, and the client marks its entry synced.

Selling and abandoning the same request both write a guard document for the request id (`request_guards`) in their transactions, so they cannot both commit; MongoDB retries the loser, which then sees the winner. Without the guard a sale and its abandonment could both succeed.

A recorded sale whose KY forms were refused is closed with `kind: "ky_forms"`, which keeps the forms that will not be recorded and links them to the bill. Repeating an abandonment returns the first record with `Idempotent-Replayed: true`. `GET /sales/abandoned` (ADMIN+) lists abandonments for audit.
