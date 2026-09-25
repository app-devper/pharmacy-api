# Durable end-of-day close

End-of-day close is a Sales command that records the covered business period, responsible user, and close time; it is not merely an on-demand report calculation. A sale made before close but confirmed afterward belongs to its cashier-submission business day and adds an auditable adjustment instead of silently rewriting the close record. The current API only computes `GET /report/eod`, so the durable close contract and KMP's `POST /report/eod/close` call still need implementation.
