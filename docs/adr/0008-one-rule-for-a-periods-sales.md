# One rule for a period's sales

Every report and the End-of-day close ([ADR-0006](./0006-end-of-day-close-snapshots-the-day.md)) compute sales through the `reporting` package. Before, the dashboard summary and the close summed bill totals in the pharmacy's timezone, while the daily, monthly, profit and top-drug reports summed line subtotals before the bill discount and grouped days in UTC — so one day could show three different figures, and sales between 00:00 and 07:00 in Bangkok landed on the previous day.

- A period is `[from, to)`; days and months are calendar dates in the pharmacy's timezone.
- A sale counts when it was confirmed (`sold_at`) unless voided; a return counts when it was made.
- An amount is what the customer paid: a bill discount is spread over the bill's lines in proportion to their subtotals. A day's line totals therefore equal its bill totals less refunds, which is the End-of-day figure, and per-drug profit no longer overstates discounted bills.
- A return refunds each line's paid share (`price × qty × total ÷ (total + discount)`). Returns recorded before this decision keep their refund.

Drugs at or below zero stock count as out of stock, and stock value counts only units on hand. Closed days need no special handling: their snapshot plus adjustments equals the live figure.
