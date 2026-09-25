# Read UM sessions from Redis

Supersedes the "do not share Redis" clause of [ADR-0001](./0001-live-identity-for-pharmacy-operations.md); its bounds still hold (cache at most 30 seconds per session, revocation within 60 seconds, stop writes and sensitive reads without a current answer, catalog reads may continue under a valid signed token).

This service reads `session:<jti>` from UM's Redis instead of calling UM's verify endpoint. A missing key means the session was logged out, revoked, or expired; a present key must name the token's system. Redis holds no role or client, so authorization keeps using the signed token's `role` and `clientId`: UM revokes every session of a user whose role, status, password, or account changes (UM ADR-0003), so a live session vouches for its token's claims. The key layout is UM's contract, pinned by a UM test. This service connects with read-only intent and never writes UM keys.
