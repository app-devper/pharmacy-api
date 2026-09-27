# Inventory owns every stock change

Every change to physical stock goes through the `inventory` package, as Sales commands go through `sales` ([ADR-0005](./0005-commercial-commands-run-once-in-the-sales-module.md)): receiving a lot (goods receipt or a lot entered by hand), taking and giving back stock for sales, returns and voids, adjustments, stock counts, write-offs, deleting a mistaken lot, and settling oversold stock. HTTP handlers only translate requests. Seven handlers used to apply these rules by hand, inconsistently: a lot entered by hand did not settle oversold sales, adjustments and counts changed stock without lots, deleting a lot left no record, and a written-off lot was deleted so later voids and returns of its bills failed.

For a drug that has lots, each command keeps `drug.stock = Σ lot.remaining − Σ unsettled oversold quantity` in its own transaction and writes its audit record. A drug with no lots at all (records from before lots) keeps stock alone. The module applies changes as deltas; it never recomputes stock from lots, so disagreement already in the data neither grows nor is silently corrected. `GET /inventory/drift` (ADMIN+) lists lot-tracked drugs that disagree, for the pharmacy to decide.

- **Decrease** (adjustment, count below system stock): taken from sellable lots, first expiry first.
- **Increase**: put into the named lot (existing, or a new lot with number and expiry), then oversold sales are settled from it. Until the clients send a lot, an unnamed increase goes to the latest-expiring lot in use and the adjustment is flagged `lot_assumed`; a follow-up makes the lot required.
- **Write-off**: all listed lots or none. A written-off lot is kept at zero with `written_off_at`, never deleted. Goods returned or voided later go back to it (they are physically back) but a sale never takes from it.
- **Delete lot**: only for a lot no sale has taken from (entered by mistake); recorded with reason `deleted`. Otherwise 409: write it off.
- **Bulk import** with stock creates an `OPENING` lot with no expiry, taken first and excluded from expiry lists.

Stock movement stays a view over these records ([KMP ADR-0003](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/adr/0003-current-stock-and-movement-view.md)); this decision adds no ledger. Integration tests run against a MongoDB replica set when `MONGO_TEST_URI` is set.
