# Inventory answers stock movements

Extends [ADR-0007](./0007-inventory-owns-every-stock-change.md); keeps KMP ADR-0003 (movements are a view of business records, not a ledger).

`GET /movements` is answered by `inventory.Movements`, the module that writes every stock change, so a period's movements for a drug add up to how its stock changed. The handler only parses the query and pages the result.

Before, the handler rebuilt the view itself and counted every lot created in the period as an import. A stock increase or count naming a new lot creates that lot with the increase as its quantity and also records the adjustment, so the increase showed twice.

Each lot now records its origin: `received` (a confirmed import or a lot added by hand), `opening` (stock a drug was created with, written through `inventory.OpenStock`), or `adjustment` (created by an increase). Only received and opening lots are imports. Lots made before origins were recorded count as made by an increase when an adjustment referencing them was written within a minute of the lot.

Deleting a lot nothing was sold from undoes its receipt: the lot drops out of the view, and the deletion's writeoff records how much its receipt put in (`received`) so it shows only the rest, such as an increase into that lot. Deletions recorded before `received` existed are hidden, having undone exactly their receipt.
