# Money is settled to the satang

Every money amount a Sale or a Return produces is rounded to two decimals, half away from zero, exactly once at the point it is born, and is never re-rounded in place. A cashier who pays the total the client displayed is never refused over a sub-satang remainder.

- A unit price is settled where it is taken from the catalog (`catalog.LinePrice`). A line subtotal is `satang((price − item discount) × qty)`, and a sale subtotal is `satang(Σ line subtotals)`.
- A bill discount is `satang(clamp(amount, 0, subtotal))`. A percentage is resolved into an amount by the client before it is sent, so the server only ever sees a two-decimal amount.
- The total is `satang(subtotal − discount)`, and change is `satang(received − total)`.
- A Return's refund is settled per line as `satang(qty × price × paid share)`, and the refund total as `satang(Σ lines)`.
- `received` is accepted when `satang(received) >= total`. This rule exists because a repeating per-unit item discount, such as a third of a baht, once made the server's total exceed the rounded total the client showed.
- Clients display the same figures, as `PricedBill` in pharmacy-app-kmp does (glossary term "Satang rounding"). The Paid figure in `ky.go` already follows this rule.
