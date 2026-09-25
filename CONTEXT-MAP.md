# Pharmacy API context map

This service commits pharmacy business changes for multiple tenants. The [product-wide context map](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/CONTEXT-MAP.md) defines the shared vocabulary; this file identifies the contexts implemented by this API.

## Contexts

- [Sales](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/sales/CONTEXT.md) — confirms sales, returns, and voids; its end-of-day close is an agreed target that is not yet implemented.
- [Inventory](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/inventory/CONTEXT.md) — owns stock quantities, lots, stock counts, adjustments, and movement views.
- [Catalog](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/catalog/CONTEXT.md) — owns drug identity, barcodes, and configured prices.
- [Purchasing](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/purchasing/CONTEXT.md) — owns purchase orders, suppliers, and receipt confirmation.
- [Compliance](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/compliance/CONTEXT.md) — owns KY records and the target review of skipped capture.
- [Customers](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/customers/CONTEXT.md) — owns customer identity and profile; spend and visit summaries derive from Sales.
- [Store Configuration](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/store-configuration/CONTEXT.md) — owns settings shared by one pharmacy tenant.
- [Access](https://github.com/app-devper/pharmacy-app-kmp/blob/develop/docs/contexts/access/CONTEXT.md) — an external context owned by `um-api`; this service enforces its permissions for pharmacy operations.

## Relationships

- **Sales → Inventory**: a confirmed sale, return, or void changes stock in the same backend transaction.
- **Purchasing → Inventory and Compliance**: confirming goods receipt creates lots and stock changes, reconciles oversold quantities, and creates KY9 records.
- **Sales → Customers**: confirmed sales and reversals contribute to customer spend and visit summaries.
- **Identity service → pharmacy API**: `um-api` owns accounts, sessions, and role assignment; this API enforces operation permissions within the tenant selected by the signed identity context.
- **Pharmacy API → KMP**: this API confirms business outcomes. Locally queued client operations remain pending until confirmed here.

The [architecture notes](./docs/ARCHITECTURE-NOTES.md) distinguish current behavior from decisions that still need implementation.
