# Money is settled to the satang

Every money amount a Sale or a Return produces is rounded to two decimals, half away from zero (`math.Round(x*100)/100`), exactly once at the point it is born, and is never re-rounded in place. Amounts stored before this rule are never re-rounded. A cashier who pays the total the client displayed is never refused over a sub-satang remainder.

- Alternative-unit per-base prices are settled in `catalog.LinePrice`; base prices are entered to the satang. A line subtotal is `satang((price − item discount) × qty)`, and a sale subtotal is `satang(Σ line subtotals)`.
- A bill discount is `satang(clamp(amount, 0, subtotal))`. A percentage is resolved into an amount by the client before it is sent, and the server settles whatever amount it receives (the deployed client sends unrounded percent-derived discounts).
- The total is `satang(subtotal − discount)`, and change is `satang(received − total)`.
- A Return's refund is settled per line by its running total: the line's refund is `satang((returned + qty) × price × paid share) − satang(returned × price × paid share)`, so a line's refunds never exceed what it paid. The refund is `satang(Σ lines)`, capped at what is still refundable on the Sale (`satang(total − refunded)`), so the refunds of a Sale never add up to more than the customer paid.
- `received` is accepted when `satang(received) >= total`. This rule exists because a repeating per-unit item discount, such as a third of a baht, once made the server's total exceed the rounded total the client showed.
- Clients display the same figures, as `PricedBill` in pharmacy-app-kmp does (glossary term "Satang rounding"). The Paid figure in `ky.go` already follows this rule.
- Until a client adopts the rule, one that sums unrounded lines may differ by a satang on multi-line carts with sub-satang discounts, and a cashier typing the displayed total may be refused; paying "exact" (received 0) always works. pharmacy-app-kmp PR #457 rounds each line first, matching this server.
