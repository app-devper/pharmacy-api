# Commercial commands run once in the sales module

Implements [ADR-0002](./0002-idempotent-commercial-commands.md) for returns and moves every Sales command out of the HTTP handlers.

The `sales` package owns confirming a Sale, recording a Return, and voiding a Sale: pricing, first-expiry-first lot deduction and restoration, bill and return numbering, and customer spend, each in one transaction. Handlers only decode requests and render outcomes. End-of-day close ([ADR-0003](./0003-durable-end-of-day-close.md)) will be added here.

A **Commercial command** carries an optional `client_request_id`. The id is stored on the command's own record (the sale or the return) under a partial unique index, together with a fingerprint of the request without its id. A repeat with the same id and the same request returns the recorded outcome with `Idempotent-Replayed: true`, even when it arrives concurrently with the first attempt; the same id with a different request is refused with 409, so a client bug cannot silently swallow a different sale or return. Sales recorded before fingerprints existed are treated as matching. A replayed sale has no `stock_updates`.

Void is not keyed by request id: voiding twice is already a 409 and cannot reverse stock twice.

Returns accept requests without an id until pharmacy-web and KMP send one; a follow-up makes it required once the KMP release is deployed.

The behaviour is pinned by integration tests against a MongoDB replica set, since transactions and unique indexes are what make it hold.
