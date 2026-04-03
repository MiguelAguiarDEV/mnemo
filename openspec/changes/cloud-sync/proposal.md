# Proposal: Cloud Sync -- Team Memory Sharing via Postgres Backend

## Intent

Mnemo is currently a single-user, local-only memory system. Every observation lives in a SQLite database on one machine (`~/.mnemo/mnemo.db`), and synchronization is limited to filesystem-based chunk export/import through the `internal/sync/` package. This creates three concrete problems:

1. **No team collaboration.** Developers cannot share learned patterns, architectural decisions, or institutional knowledge across a team. Each engineer's memories are siloed in their own SQLite database. When one developer discovers a gotcha about the project's API layer, that knowledge dies on their machine.

2. **No cross-machine access.** A developer's own memories are locked to the device where they were created. Moving between a work laptop and a home desktop means starting from zero context, even though the same developer produced both sets of observations.

3. **No centralized search.** There is no way to query the collective memory of a team. The existing `mnemo search` command and `/search` HTTP endpoint only query the local SQLite store. A team lead cannot ask "what has the team learned about our payment integration?" across all contributors.

This change adds a **cloud sync layer** on top of the existing local-first architecture. The local SQLite database remains the primary source of truth for each developer. A new cloud server mode (same `mnemo` binary, new `--mode cloud` flag) backed by PostgreSQL enables teams to push/pull observations and search shared team memory. JWT authentication secures the cloud endpoints.

**Why now:** Mnemo's chunk-based sync system (`internal/sync/sync.go`) already produces content-addressed, append-only `.jsonl.gz` chunks with a SHA-256-prefix ID and a `manifest.json` index. This is a natural transport format for cloud sync -- we extend the existing sync infrastructure with an HTTP-based transport rather than replacing it. The `ChunkData` struct (sessions + observations + prompts) is already the serialization unit. The `Manifest` and `ChunkEntry` types already track provenance (`created_by`, `created_at`). Adding a remote transport is the logical next step.

**Why Postgres:** The existing SQLite FTS5 search is excellent for single-user local queries, but Postgres offers `tsvector`/`tsquery` full-text search that scales to team-sized datasets (tens of thousands of observations), supports concurrent writers (multiple team members pushing simultaneously), and provides built-in connection pooling. Postgres is also the most commonly self-hosted relational database, making it accessible for teams that already run infrastructure.

## Scope

### In Scope

1. **Postgres store** (`internal/cloud/pgstore/`) -- A new package implementing observation storage, session management, and full-text search against PostgreSQL. Uses `pgx/v5` as the driver. Implements the same data model as `internal/store/store.go` (sessions, observations, prompts) but with Postgres-native types and `tsvector` FTS instead of SQLite FTS5. Includes a `team_id` column on all tables for multi-tenant isolation.

2. **JWT authentication middleware** (`internal/cloud/auth/`) -- A new package providing HTTP middleware for JWT token validation. Supports both Bearer tokens (for programmatic access) and a simple API key fallback (for initial setup/testing). Uses `golang-jwt/jwt/v5` for token parsing and validation. Extracts `team_id` and `user_id` claims from the JWT payload to scope all queries.

3. **Cloud server mode** (`mnemo cloud serve`) -- Adds an explicit cloud-only CLI entrypoint in `cmd/mnemo/main.go` instead of overloading the local `serve` command. This keeps local SQLite serving and cloud Postgres serving clearly separated while still shipping in the same binary. The cloud server exposes the sync/auth/search/context surface needed for remote chunk sync (`POST /sync/push`, `GET /sync/pull`, `GET /sync/pull/{chunk_id}`, `GET /sync/search`, `GET /sync/context`).

4. **Remote sync transport** -- Extends `internal/sync/sync.go` with a `Transport` interface that abstracts where chunks are read from and written to. The existing filesystem-based sync remains the default behind `New(s, syncDir)`, with `NewLocal(s, syncDir)` kept as a clearer alias. A new `RemoteTransport` implements HTTP-based push/pull against the cloud server, and `NewWithTransport(s, transport)` wires custom transports into the `Syncer`. The `mnemo sync --remote <url> --token <jwt>` flags configure the remote transport.

5. **CLI integration** -- New flags on the `sync` subcommand: `--remote <url>` (cloud server URL), `--token <jwt>` (authentication token). New `mnemo cloud` subcommands cover server startup and credential workflows: `mnemo cloud serve`, `mnemo cloud register`, `mnemo cloud login`, `mnemo cloud sync`, `mnemo cloud status`, and `mnemo cloud api-key`.

6. **Cloud search** -- Full-text search over team observations using Postgres `tsvector`/`tsquery`. Exposes the same `/search` endpoint interface but backed by Postgres FTS. Supports the same query parameters (`q`, `type`, `project`, `scope`, `limit`) for API compatibility.

### Out of Scope (Deferred)

1. **Real-time sync (WebSockets)** -- Push-based live updates when team members save observations. This would require a persistent connection layer and is better addressed after the basic push/pull model is validated.

2. **Multi-tenant SaaS platform** -- While `team_id` is included in the schema for isolation, we are NOT building tenant management, billing, or onboarding flows. Teams manually configure their `team_id`.

3. **Admin UI / web dashboard** -- No web frontend for browsing team memories. The CLI and MCP tools remain the primary interface.

4. **SQLite-to-Postgres migration tool** -- No automated migration of existing local SQLite data to a cloud Postgres instance. Users can export via `mnemo export` and re-import if needed.

5. **End-to-end encryption of observations** -- Observations are stored in plaintext on the Postgres server. E2E encryption would require client-side key management and would prevent server-side FTS.

6. **CRDT conflict resolution** -- The chunk-based sync model is append-only by design (each sync creates a NEW chunk, never modifies old ones). Conflict resolution is not needed for this model. If two users push chunks with overlapping observations, the import deduplication logic (content hash) handles it.

7. **Store interface extraction (Path A refactor)** -- We are NOT extracting a common interface from `internal/store/store.go` that both SQLite and Postgres implement. The Postgres store is a separate package under `internal/cloud/` with its own types. This avoids touching ANY existing code in `internal/store/`.

## Approach

### Architecture: Path B -- Additive Cloud Layer

The cloud sync feature is implemented as a purely additive layer. Zero changes to `internal/store/store.go`. The rationale:

- `internal/store/store.go` is 900+ lines of battle-tested SQLite code with FTS5, deduplication, import/export, and session management. Refactoring it to share an interface with Postgres introduces risk to every existing user.
- The cloud store has different requirements: `team_id` scoping, JWT-derived user context, Postgres-specific FTS syntax (`tsvector` vs FTS5), connection pooling.
- By keeping them separate, a bug in the cloud layer cannot affect local-only users.

### Same Binary, Explicit Cloud Subcommand

The `mnemo` binary keeps local and cloud serving in the same executable, but uses a dedicated cloud subcommand instead of a `--mode` flag:

```
mnemo serve                    # Local mode (default) -- SQLite, no auth
mnemo cloud serve              # Cloud mode -- Postgres, JWT auth required
```

Cloud mode reads Postgres connection details from environment variables:

```
MNEMO_DATABASE_URL=postgres://user:pass@host:5432/mnemo
MNEMO_JWT_SECRET=<secret>
```

### Chunk-Based HTTP Transport

The existing `Syncer` works with `ChunkData` (sessions + observations + prompts serialized as JSON). The remote transport sends these same chunks over HTTP:

- **Push**: `POST /sync/push` with the gzipped chunk as the request body and the `ChunkEntry` metadata as headers. The cloud server stores the chunk in Postgres (decomposing it into individual sessions/observations/prompts rows) and records the chunk ID in a `sync_chunks` table.
- **Pull**: `GET /sync/pull` returns manifest metadata for the authenticated user's chunks, and the client fetches each chunk body individually via `GET /sync/pull/{chunk_id}`.

### Postgres Schema

The cloud Postgres schema mirrors the SQLite schema but with Postgres-native types:

- `sessions` table with `team_id TEXT NOT NULL` column
- `observations` table with `team_id TEXT NOT NULL` column and a `search_vector TSVECTOR` column maintained by a trigger
- `prompts` table with `team_id TEXT NOT NULL` column
- `sync_chunks` table tracking which chunks have been received, with `team_id` scoping
- GIN index on `search_vector` for fast FTS queries
- All queries are scoped by `team_id` extracted from the JWT

### JWT Authentication

- All cloud endpoints require a valid JWT in the `Authorization: Bearer <token>` header
- JWT payload contains `team_id`, `user_id`, and `exp` (expiration)
- `mnemo cloud register` and `mnemo cloud login` mint JWTs signed by the server and persist the access token in `~/.mnemo/cloud.json`
- Middleware extracts claims and injects them into the request context
- Every Postgres query includes `WHERE team_id = $1` using the JWT-derived team ID

### Dependency Additions

Three new dependencies in `go.mod`:

- `github.com/jackc/pgx/v5` -- Postgres driver (widely used, performant, maintained)
- `github.com/golang-jwt/jwt/v5` -- JWT parsing and validation (standard library for Go JWT)
- `golang.org/x/crypto` -- For bcrypt password hashing if needed for token generation

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `internal/cloud/pgstore/` | **New** | Postgres-backed observation store with tsvector FTS, team_id scoping, session/observation/prompt CRUD |
| `internal/cloud/auth/` | **New** | JWT middleware, token generation, claims extraction |
| `internal/cloud/server/` | **New** | Cloud-specific HTTP handlers (sync push/pull/status), wraps pgstore |
| `internal/sync/sync.go` | **Modified** | Add `Transport` interface, extract `FileTransport`, add `RemoteTransport`. The `Syncer` struct gains a `transport` field. Existing `Export`/`Import` methods delegate to the transport. |
| `cmd/mnemo/main.go` | **Modified** | Add `mnemo cloud` subcommands (serve/register/login/sync/status/api-key) and `--remote`/`--token` flags to `cmdSync` |
| `go.mod` | **Modified** | Add `pgx/v5`, `golang-jwt/jwt/v5`, `golang.org/x/crypto` |
| `internal/store/store.go` | **Untouched** | Zero changes. Local SQLite store remains identical. |
| `internal/server/server.go` | **Untouched** | The existing local HTTP server is unchanged. Cloud mode uses its own server package. |
| `internal/mcp/mcp.go` | **Untouched** | MCP tools continue to use the local store. Cloud search is accessed via HTTP, not MCP (for now). |
| `internal/tui/` | **Untouched** | TUI remains local-only. |
| `internal/setup/` | **Untouched** | Agent setup plugins are unaffected. |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| **Postgres FTS vs SQLite FTS5 behavior differences** -- tsvector tokenization, ranking (ts_rank vs bm25), and query syntax differ from FTS5. Search results may differ between local and cloud. | **High** | Build a dedicated compatibility test suite that runs the same queries against both backends and asserts equivalent result sets. Document known differences. Use `ts_rank_cd` with normalization for closest BM25 approximation. |
| **sync.go modification breaks existing file-based sync** -- The Transport interface extraction touches the one file that bridges local and shared data. | **Medium** | The refactor is purely structural (extract interface, existing code becomes FileTransport). All existing sync_test.go tests MUST pass without modification. Add interface-level tests for both transports. |
| **JWT secret management** -- Users may hardcode secrets in shell history or config files. | **Medium** | Document secure practices. Support `MNEMO_JWT_SECRET` env var (not CLI flag). Recommend using a secrets manager. Token rotation is out of scope but the schema supports it. |
| **Postgres availability/setup complexity** -- Teams need to provision and maintain a Postgres instance. | **Medium** | Provide a `docker-compose.yml` for quick local testing. Document minimum Postgres version (14+). `mnemo cloud serve` runs schema initialization automatically on startup. |
| **Network latency on sync** -- Large chunk uploads/downloads over slow connections. | **Low** | Chunks are already gzip-compressed. The push/pull model is async (not blocking the developer's workflow). Add timeout configuration. |
| **Data consistency between local and cloud** -- If a developer saves locally but forgets to push, their team doesn't see the observation. | **Low** | This is by design (local-first). Document the workflow clearly. Consider adding a reminder in session end summary (deferred). |

## Rollback Plan

The rollback is clean and low-risk because of the additive (Path B) approach:

1. **Remove `internal/cloud/`** -- Delete the entire `internal/cloud/` directory tree (pgstore, auth, server). This removes all Postgres and JWT code.

2. **Revert `internal/sync/sync.go`** -- Undo the Transport interface extraction. This restores the file to its current state where `Export` and `Import` directly use filesystem operations.

3. **Revert `cmd/mnemo/main.go`** -- Remove the cloud subcommands and the `--remote`/`--token` flags from `cmdSync`.

4. **Remove dependencies from `go.mod`** -- Run `go mod tidy` after removing imports to clean up `pgx/v5`, `golang-jwt/jwt/v5`, and `golang.org/x/crypto`.

5. **Verify** -- Run `go test ./...` to confirm all existing tests pass. Run `go build ./...` to confirm the binary builds. The local-only workflow is completely unaffected because `internal/store/`, `internal/server/`, `internal/mcp/`, `internal/tui/`, and `internal/setup/` are never modified.

**Estimated rollback time**: Under 30 minutes. All changes are either new files (delete) or isolated modifications to two existing files (revert).

## Dependencies

- **PostgreSQL 14+** -- Required for the cloud server mode. Not required for local-only usage. Postgres 14 introduced performance improvements to GIN indexes that benefit tsvector search.
- **Network access** -- The remote sync transport requires HTTP connectivity between the client and cloud server. No specific port requirements beyond what the operator configures.
- **Go 1.22+** -- Already required by the project (`go.mod` specifies `go 1.25.0`). The `net/http` ServeMux pattern matching used in `internal/server/server.go` requires Go 1.22+.
- **No external services** -- No dependency on AWS, GCP, or any cloud provider. The Postgres instance can be self-hosted, run in Docker, or hosted on any managed Postgres service (RDS, Cloud SQL, Supabase, Neon, etc.).

## Success Criteria

- [ ] `mnemo cloud serve` starts a Postgres-backed server that responds to `/health` with `{"status":"ok","service":"mnemo-cloud"}` when healthy
- [ ] `mnemo cloud serve` initializes the Postgres schema on startup and serves the authenticated cloud API
- [ ] `mnemo cloud register` / `mnemo cloud login` obtain a valid JWT that the auth middleware accepts
- [ ] `mnemo sync --remote <url> --token <jwt>` pushes local chunks to the cloud server via HTTP
- [ ] `mnemo sync --remote <url> --token <jwt> --import` pulls new chunks from the cloud server into the local SQLite database
- [ ] JWT authentication rejects requests without a valid token (401), rejects expired tokens (401), and scopes all queries to the token's `team_id`
- [ ] Cloud search (`GET /search?q=...`) returns results from team observations using Postgres tsvector FTS, with the same response format as the local search endpoint
- [ ] The local-only workflow (`mnemo serve`, `mnemo mcp`, `mnemo sync`, `mnemo search`, all existing tests) is completely unaffected -- zero regressions
- [ ] All new code in `internal/cloud/` has >= 80% test coverage as measured by `go test -cover`
- [ ] Existing `internal/sync/sync_test.go` tests pass without modification after the Transport interface extraction
- [ ] A `docker-compose.yml` is provided for spinning up a local Postgres instance for testing
