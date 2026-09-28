# Receiving owns goods receipt

Goods receipt (purchase orders) moves from the import handlers into a `receiving` module, like `sales` and `inventory`: `Draft`, `Revise`, `Discard`, and `Confirm`, refusing with `refusal` errors.

- One rule checks a line at every step: quantity above zero, prices not negative, the drug exists, and an expiry, when given, is a date. A draft may leave the lot number and expiry for later; `Confirm` needs them on every line and names every line that lacks them in one refusal, before writing anything.
- Drug name, registration number and unit always come from the catalog. A `drug_name` sent by a client is accepted but ignored; it used to override the catalog name on the lot and in the ขย.9 register.
- `Confirm` receives each line through `inventory.ReceiveLot` and records its ขย.9 row, then marks the order confirmed, in one transaction; confirming, revising or discarding a confirmed order is a 409. The ขย.9 row is built by `models.Ky9Input.Record`, the same builder a manual `POST /ky9` uses.

Returns get the same treatment in `sales`: `sales.Returnable` is the one rule for how much of a sale line can still be returned (units not sold from a lot, oversold or settled from bulk stock, cannot), `Return` enforces it once inside its transaction, and `GET /sales/{id}/items` reports `returned_qty`, `returnable_qty` and `unlinked_qty` from it, so clients stop computing it. Asking for more than the line allows is a 400; the lot-backed cap used to surface as a 500. A line of a drug without lots, which the old cap refused entirely, returns to stock alone.
