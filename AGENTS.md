# AGENTS.md

## Scope
This file applies to the backend repository at `/Users/admin/ProjectPos/pharmacy-app/backend`.

This backend is a separate Git repository inside the `pharmacy-app` workspace. Run repository commands from this directory, not from the workspace root.

## Project Overview
- Standalone Pharmacy POS REST API for `pharmacy-app`.
- Stack: Go 1.25, Chi v5, MongoDB Go Driver v2, JWT HS256 auth.
- Multi-tenant by `clientId`; each tenant maps to its own MongoDB database.
- Auth tokens are issued by the external Um-Api, but this service verifies JWTs locally with `SECRET_KEY`; do not add outbound Um-Api calls.
- There is no Redis/cache layer. Tenant Mongo handles are cached in-process by the DB manager.
- The frontend consumes this API, so API contract changes may require synchronized frontend updates.

## Repository Layout
- `main.go` — dependency wiring, server startup, default tenant bootstrap.
- `config/` — environment loading.
- `db/` — multi-tenant Mongo manager, indexes, seed/bootstrap logic.
- `middleware/` — CORS, JWT authentication, RBAC authorization.
- `handlers/` — flat package with one file per resource or workflow.
- `models/` — BSON/JSON entities.
- `routes/` — Chi router setup in `routes.go`.
- `pdf/` — GoFPDF renderers for KHY forms and receipts.
- `README.md` — full API reference; keep it synchronized with API changes.

Keep this repository layout flat. Do not introduce a nested `app/features/...` architecture.

## Request Flow
- `main.go` loads config, connects MongoDB, bootstraps the default tenant, constructs handlers, and calls `routes.Setup`.
- `routes/routes.go` builds the Chi router, applies logging/recovery/CORS, then protects `/api/pharmacy/v1` with JWT middleware.
- `middleware/auth.go` validates HS256 JWTs and stores role, system, tenant, and session identifiers in request context.
- `middleware/authorize.go` enforces role ordering with `RequireRole`.
- Handlers resolve the current tenant through middleware helpers and then use the tenant database from `db.MongoManager`.

## Architecture Rules
- Keep the current flat layout; do not introduce an `app/features/...` structure.
- API base path is `/api/pharmacy/v1`; routes are auth-protected.
- Roles are ordered `USER < ADMIN < SUPER`; `RequireRole(min)` allows roles at or above `min`.
- Handlers should get the tenant from request context via middleware helpers, not from request bodies.
- Preserve multi-tenant isolation; never hardcode a tenant database except for the existing default tenant behavior.
- Validate `clientId` consistently with the DB manager rules before constructing database names.
- Add new endpoints in `routes/routes.go` under the correct role group. Prefer `USER+` only for read/basic workflows and `ADMIN+` for writes, imports, stock, financial, KHY, settings-write, and exports.
- Keep dependency injection explicit through `main.go` and handler constructors; avoid package-level mutable state.
- Prefer MongoDB transactions for multi-document writes that must stay consistent, especially stock, lots, sales, returns, imports, and movements.
- Keep response shapes backward-compatible unless the user explicitly asks for a breaking API change.
- Use `primitive.ObjectID` consistently for Mongo document identifiers and validate path/body IDs before DB operations.
- Return JSON error responses in the style used by adjacent handlers rather than introducing a new error envelope.

## Domain Invariants
- Preserve stock consistency: `drug.stock ≈ sum(lot.remaining) - sum(oversold_qty)`.
- Follow existing reconcile helpers for stock, lots, sales, returns, and oversell behavior instead of creating parallel logic.
- Respect FEFO behavior when changing sales or stock depletion paths.
- Synthetic `ADJUST:` lot quantities are not returnable as real sale lots.
- Use the existing timezone helper for date math; do not rely on `time.Local`.
- Keep KHY compliance fields and PDF behavior aligned with existing handlers, models, and renderers.
- `client_request_id` idempotency for sales is intentional; preserve it for offline/queued frontend flows.
- Customer phone, drug barcode, and sale client request IDs rely on partial unique indexes that exclude empty values; do not replace them with strict non-partial uniqueness without checking product behavior.
- Bulk import is intentionally more permissive than single-drug creation; preserve tested differences unless the task explicitly changes import rules.
- Sales, voids, returns, stock adjustments, lot write-offs, and import confirmations must leave an auditable movement/history trail when existing code does so.
- KHY9 is purchase-report oriented; KHY10–KHY12 can require order-level compliance fields on sales.
- When changing receipt, report, or KHY date ranges, use tenant settings timezone via `loadTimezone`.

## Multi-Tenancy
- Default tenant `clientId == "000"` maps to the bare `DB_PREFIX` database name.
- Other tenants map to `<DB_PREFIX>_<clientId>`.
- Tenant IDs are limited to safe alphanumeric, underscore, and hyphen values by DB manager validation.
- Per-tenant initialization and indexes are handled by `db/mongo.go`; update index definitions there when adding query patterns or uniqueness guarantees.
- Do not share data across tenants in handlers, reports, imports, exports, or background-style reconciliation logic.

## Environment
- Configuration is loaded through `config/config.go`; trust code over docs when they differ.
- Required on startup: `SECRET_KEY`, `SYSTEM`.
- Defaults exist for `MONGO_URI`, `DB_PREFIX`, and `PORT`.
- `DB_NAME`, `FRONTEND_ORIGIN`, and `UM_API_URL` are not used by current backend logic.
- Do not commit new secrets or depend on local `.env` values as source of truth.
- Local `.env` may exist for convenience, but code changes should rely on `config.Load` behavior.
- CORS behavior currently lives in middleware code, not in environment configuration.

## API And Handler Conventions
- Keep handlers focused on HTTP parsing, validation, orchestration, and response writing.
- Keep BSON/JSON field tags in models synchronized with API payloads and Mongo documents.
- Prefer adding small helper functions near related handler code when validation or payload-building needs tests.
- Validate request bodies strictly enough to prevent invalid stock, price, quantity, expiry, role, tenant, or date states.
- For list endpoints, preserve existing query parameter names and pagination/filter semantics when present.
- For PDF/export endpoints, keep Thai text, KHY labels, date formatting, and file download behavior consistent with existing renderers.
- If an endpoint change affects frontend expectations, document it in `README.md` and mention the likely frontend impact.

## Commands
- Download dependencies: `go mod download`.
- Run server: `go run main.go`.
- Run all tests: `go test ./...`.
- Run handler tests: `go test ./handlers`.
- Run one test: `go test -run TestBuildDrugCreatePayload ./handlers`.
- Format touched Go files: `gofmt -w <file>`.

Prefer running the narrowest relevant test first, then broader tests if the change is larger.

## Testing Guidance
- Prefer unit tests; do not add testcontainers or tests requiring a real MongoDB unless explicitly requested.
- Follow existing isolated handler test patterns, such as payload validation tests.
- Bulk import behavior intentionally differs from single-drug create; do not “fix” that asymmetry unless requested.
- For non-trivial backend changes, run `go test ./...` when practical.
- Add or update tests when changing pure validation, payload construction, role logic, date math helpers, or stock calculation helpers.
- Do not add a new test framework unless the user explicitly asks.
- If tests cannot be run because of sandbox, dependency, or environment limits, state that clearly in the final response.

## Documentation
- Update `README.md` when endpoints, request/response payloads, auth behavior, environment variables, or operational commands change.
- Avoid duplicating the full API reference in this file.
- Keep `AGENTS.md` focused on agent workflow and repository-specific guardrails.
- Prefer examples in `README.md`; prefer rules and rationale in `AGENTS.md`.

## Change Boundaries
- Make minimal, localized changes that match existing naming, package boundaries, and handler style.
- Do not refactor unrelated handlers or models while fixing a specific bug.
- Do not rename public JSON fields, route paths, query params, or collection names unless explicitly requested.
- Do not change auth, tenancy, stock, or KHY behavior as a side effect of unrelated cleanup.
- Do not remove existing Thai labels or compliance fields without confirming product/legal impact.

## Generated Artifacts
- Treat the `pharmacy-server` binary as a stale/generated artifact; ignore it unless the user explicitly asks to rebuild or remove it.
- Do not commit generated build outputs unless explicitly requested.

## Common Pitfalls
- Do not add HTTP calls to Um-Api for auth; JWT verification is local.
- Do not use `time.Local` for reports, receipts, KHY forms, or dashboard date ranges.
- Do not assume empty barcode or customer phone must be unique; partial indexes intentionally allow multiple empty values.
- Do not allow stock-changing paths to skip lot, oversell, return, movement, or reconciliation rules.
- Do not make tests depend on a real MongoDB instance.
- Do not treat the checked-in `.env` or `pharmacy-server` binary as authoritative source files.
