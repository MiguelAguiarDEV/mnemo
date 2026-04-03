# Design: Cloud Sync

## Technical Approach

Cloud sync adds a hosted Postgres-backed server mode to Mnemo so teams can share memories across machines without git. Local SQLite instances push/pull compressed JSONL chunks over HTTP to a cloud server that stores them in Postgres with tsvector FTS. This reuses the existing chunk-based sync format (`internal/sync`) but replaces the filesystem transport with an HTTP transport, and adds a new `internal/cloud/` package tree for Postgres storage, auth, and cloud-specific server routes.

The approach deliberately keeps cloud concerns separate from the local SQLite store. The existing `internal/store/` package is deeply coupled to SQLite (FTS5 VIRTUAL TABLE, `datetime('now')`, PRAGMA statements, `modernc.org/sqlite` driver). Rather than abstracting behind an interface (which would require rewriting 15+ methods), the cloud server gets its own storage layer optimized for Postgres.

References: proposal at `openspec/changes/cloud-sync/proposal.md`, specs at `openspec/changes/cloud-sync/specs/`.

---

## Architecture Decisions

### Decision: Separate `internal/cloud/` Package Tree (Not Extending `internal/store/`)

**Choice**: Create a new `internal/cloud/` directory with its own `cloudstore`, `cloudserver`, and `auth` packages.

**Alternatives considered**:
1. **Store interface** -- Define a `MemoryStore` interface that both SQLite and Postgres implement. Rejected because the existing `Store` struct has 15+ public methods with SQLite-specific SQL (`datetime('now')`, FTS5 MATCH, PRAGMA, VIRTUAL TABLE, `ON CONFLICT(id) DO UPDATE`). Abstracting would mean rewriting every method signature and creating a massive interface that violates ISP.
2. **Extend `internal/store/` with a driver flag** -- Add `cfg.Driver = "postgres"` and switch SQL per-driver inline. Rejected because it would make store.go (already 1900+ lines) unmaintainable, and Postgres SQL differs fundamentally (tsvector, `$1` params, `NOW()`, `GENERATED ALWAYS`).
3. **Use an ORM** -- Rejected. The codebase uses raw `database/sql` everywhere. Introducing an ORM for one feature breaks conventions and adds a heavy dependency.

**Rationale**: Cloud has genuinely different concerns: multi-tenancy (`team_id`), connection pooling, tsvector FTS with weighted ranking, `GENERATED STORED` columns, and `uuid` PKs. A separate package tree keeps the local SQLite path untouched (zero regression risk) and allows the cloud store to optimize for Postgres idioms.

### Decision: Transport Interface on Syncer

**Choice**: Introduce a `Transport` interface in `internal/sync/` with `FileTransport` (existing behavior) and `RemoteTransport` (new HTTP push/pull).

**Alternatives considered**:
1. **Separate sync-cloud package** -- Rejected. The export/import logic is identical; only the I/O target changes. Duplicating the entire Syncer would violate DRY.
2. **Direct HTTP calls in main.go** -- Rejected. Untestable and couples CLI to HTTP details.

**Rationale**: This is the first real interface in the codebase. The Syncer already operates on abstract "chunks" (export data, write file, read file). Extracting a Transport interface is natural and lets us test RemoteTransport with `httptest.Server` without touching the filesystem.

### Decision: Postgres tsvector with GIN Index for Full-Text Search

**Choice**: Use Postgres `tsvector` with `GENERATED ALWAYS AS ... STORED` column and GIN index. Weight title as `A`, content as `B`, type/project as `C`.

**Alternatives considered**:
1. **pg_trgm (trigram)** -- Good for fuzzy/partial matching but poor for relevance ranking. Rejected because mnemo.s search is keyword-based like FTS5.
2. **ElasticSearch/Meilisearch** -- Rejected. Adds a second service to deploy. Postgres tsvector is sufficient for the expected data volume (thousands to low millions of observations per team).
3. **FTS5 over SQLite in the cloud** -- Rejected. SQLite is not designed for concurrent multi-writer access from a web server.

**Rationale**: tsvector is Postgres-native, requires no external service, supports weighted ranking (`ts_rank_cd`), and handles the same query patterns as the existing FTS5 search. The `GENERATED STORED` column auto-updates on INSERT/UPDATE, eliminating the need for triggers.

### Decision: JWT + API Key Dual Auth

**Choice**: JWT for interactive clients (1h access token, 7d refresh token). API keys for CI/CD and headless agents.

**Alternatives considered**:
1. **API keys only** -- Simpler but no token rotation or expiry. Bad for interactive use where you want automatic refresh.
2. **OAuth2/OIDC** -- Too heavy for a dev tool. Requires an identity provider.
3. **mTLS** -- Good security but terrible DX. Certificate management is a non-starter for individual developers.

**Rationale**: JWT gives interactive clients (TUI, browser dashboard) a smooth experience with auto-refresh. API keys give headless agents (CI, Claude Code hooks) a simple `Authorization: Bearer <key>` header. Both are validated by the same middleware.

### Decision: `lib/pq` Driver (Not `pgx`)

**Choice**: Use `github.com/lib/pq` with `database/sql`.

**Alternatives considered**:
1. **pgx native** -- Better performance, richer type support. But the entire codebase uses `database/sql` patterns (the existing store uses `sql.Open`, `sql.DB`, `sql.Tx`, `sql.Rows`). Introducing pgx's native mode would create two incompatible patterns.
2. **pgx as database/sql driver** -- Possible via `pgx/stdlib`, but adds complexity for no measurable benefit at this scale.

**Rationale**: `lib/pq` maps 1:1 to the existing `database/sql` patterns in `internal/store/`. Same `sql.Open`, same `*sql.DB`, same `*sql.Rows` scanning. Developers moving between local store and cloud store see identical Go patterns. The performance difference is negligible for a memory service.

### Decision: `--mode cloud` Flag on `mnemo serve`

**Choice**: Add a `--mode` flag to the existing `serve` command. `--mode local` (default) starts the current SQLite-backed server. `--mode cloud` starts the Postgres-backed cloud server with auth middleware.

**Alternatives considered**:
1. **Separate binary `mnemo-cloud`** -- Rejected. Increases build/release complexity and confuses users about which binary to deploy.
2. **Separate `mnemo cloud-serve` subcommand** -- Viable but less discoverable. The `serve` command already exists and users expect it.

**Rationale**: A single binary with a mode flag keeps deployment simple. The cloud mode reads Postgres config from env vars and starts the cloud server stack instead of the local SQLite stack.

---

## Data Flow

### Local Push (Client -> Cloud)

```
  Local Mnemo                             Cloud Server
  ┌──────────────┐                        ┌──────────────────────┐
  │  SQLite DB   │                        │   Auth Middleware     │
  │  (store.go)  │                        │   (JWT/API key)      │
  └──────┬───────┘                        └──────────┬───────────┘
         │                                           │
         │ Export()                                   │
         v                                           v
  ┌──────────────┐    POST /cloud/push    ┌──────────────────────┐
  │   Syncer     │ ──────────────────────>│   CloudServer        │
  │ (sync.go)    │    gzipped JSONL       │   (cloudserver.go)   │
  │ + Remote     │    chunk in body       └──────────┬───────────┘
  │   Transport  │                                   │
  └──────────────┘                                   │ Import chunk
                                                     v
                                           ┌──────────────────────┐
                                           │   CloudStore         │
                                           │   (cloudstore.go)    │
                                           │   Postgres + tsvector│
                                           └──────────────────────┘
```

### Local Pull (Cloud -> Client)

```
  Local Mnemo                             Cloud Server
  ┌──────────────┐                        ┌──────────────────────┐
  │   Syncer     │   GET /cloud/pull      │   CloudServer        │
  │ + Remote     │ ──────────────────────>│   (cloudserver.go)   │
  │   Transport  │   ?since=<timestamp>   └──────────┬───────────┘
  └──────┬───────┘                                   │
         │                                           │ Export new chunks
         │    <── gzipped JSONL response ──          │
         │                                           v
         v                                 ┌──────────────────────┐
  ┌──────────────┐                        │   CloudStore         │
  │  SQLite DB   │                        │   (cloudstore.go)    │
  │  Import()    │                        │   Postgres            │
  └──────────────┘                        └──────────────────────┘
```

### Cloud Search (Direct Query)

```
  Any Client                               Cloud Server
  ┌──────────────┐                        ┌──────────────────────┐
  │  HTTP Client │  GET /cloud/search     │   Auth Middleware     │
  │  (CLI/Agent) │ ──────────────────────>│   + team_id scoping  │
  └──────────────┘    ?q=auth+middleware  └──────────┬───────────┘
                                                     │
                                                     v
                                           ┌──────────────────────┐
                                           │   CloudStore.Search  │
                                           │   ts_rank_cd(tsv,    │
                                           │     plainto_tsquery)  │
                                           └──────────────────────┘
```

---

## Package Layout (Directory Tree)

```
mnemo/
├── cmd/mnemo/
│   └── main.go                          # MODIFY: add --mode cloud flag
├── internal/
│   ├── store/
│   │   └── store.go                     # NO CHANGES (SQLite stays untouched)
│   ├── sync/
│   │   ├── sync.go                      # MODIFY: extract Transport interface
│   │   ├── transport.go                 # CREATE: Transport interface + FileTransport
│   │   └── transport_test.go            # CREATE: Transport tests
│   ├── server/
│   │   └── server.go                    # NO CHANGES (local HTTP stays untouched)
│   ├── cloud/                           # CREATE: entire directory tree
│   │   ├── cloudstore/
│   │   │   ├── cloudstore.go            # CREATE: Postgres storage (sessions, observations, prompts)
│   │   │   ├── schema.go               # CREATE: DDL, migrations, tsvector setup
│   │   │   ├── search.go               # CREATE: tsvector search with weighted ranking
│   │   │   └── cloudstore_test.go       # CREATE: dockertest-based integration tests
│   │   ├── cloudserver/
│   │   │   ├── cloudserver.go           # CREATE: Cloud HTTP server (routes, handlers)
│   │   │   ├── middleware.go            # CREATE: Auth middleware, team scoping, logging
│   │   │   ├── push_pull.go             # CREATE: Push/pull sync handlers
│   │   │   └── cloudserver_test.go      # CREATE: httptest-based tests
│   │   ├── auth/
│   │   │   ├── auth.go                  # CREATE: JWT generation, validation, claims
│   │   │   ├── apikey.go                # CREATE: API key hashing, validation
│   │   │   └── auth_test.go             # CREATE: Token lifecycle tests
│   │   └── remote/
│   │       ├── transport.go             # CREATE: RemoteTransport (HTTP push/pull client)
│   │       └── transport_test.go        # CREATE: httptest-based transport tests
│   └── mcp/
│       └── mcp.go                       # NO CHANGES (MCP always talks to local store)
└── go.mod                               # MODIFY: add lib/pq, golang.org/x/crypto (bcrypt)
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `cmd/mnemo/main.go` | Modify | Add `--mode cloud` flag to `serve`, add `MNEMO_CLOUD_*` env parsing, import cloud packages |
| `internal/sync/sync.go` | Modify | Extract Transport interface, refactor Syncer to use Transport instead of direct filesystem I/O |
| `internal/sync/transport.go` | Create | `Transport` interface definition + `FileTransport` implementation (extracts current fs logic) |
| `internal/sync/transport_test.go` | Create | Unit tests for FileTransport |
| `internal/cloud/cloudstore/cloudstore.go` | Create | Postgres-backed store: sessions, observations, prompts CRUD with `team_id` scoping |
| `internal/cloud/cloudstore/schema.go` | Create | DDL for Postgres tables, indexes, tsvector columns, migration runner |
| `internal/cloud/cloudstore/search.go` | Create | FTS via `ts_rank_cd` + `plainto_tsquery`, weighted ranking |
| `internal/cloud/cloudstore/cloudstore_test.go` | Create | Integration tests using `ory/dockertest` for real Postgres |
| `internal/cloud/cloudserver/cloudserver.go` | Create | HTTP server for cloud mode: route registration, handler setup |
| `internal/cloud/cloudserver/middleware.go` | Create | Auth middleware (JWT + API key), team_id injection, request logging |
| `internal/cloud/cloudserver/push_pull.go` | Create | Handlers for `POST /cloud/push`, `GET /cloud/pull`, `GET /cloud/manifest` |
| `internal/cloud/cloudserver/cloudserver_test.go` | Create | httptest-based handler tests |
| `internal/cloud/auth/auth.go` | Create | JWT token generation/validation, claims struct, refresh flow |
| `internal/cloud/auth/apikey.go` | Create | API key generation (crypto/rand), bcrypt hashing, validation |
| `internal/cloud/auth/auth_test.go` | Create | Token lifecycle, expiry, refresh, API key validation tests |
| `internal/cloud/remote/transport.go` | Create | `RemoteTransport` implementing `sync.Transport` via HTTP |
| `internal/cloud/remote/transport_test.go` | Create | RemoteTransport tests with httptest.Server |
| `go.mod` | Modify | Add `github.com/lib/pq`, `golang.org/x/crypto` (bcrypt), `github.com/ory/dockertest/v3` (test) |

---

## Postgres Schema

```sql
-- ─── Teams ──────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS teams (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── API Keys ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,                          -- human label: "CI key", "alan-laptop"
    key_hash    TEXT NOT NULL,                          -- bcrypt hash of the raw key
    prefix      TEXT NOT NULL,                          -- first 8 chars for identification (engr_XXXXXXXX...)
    scopes      TEXT[] NOT NULL DEFAULT '{push,pull,search}', -- permission scopes
    expires_at  TIMESTAMPTZ,                            -- NULL = never expires
    last_used   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_apikeys_prefix ON api_keys(prefix);
CREATE INDEX idx_apikeys_team   ON api_keys(team_id);

-- ─── Sessions ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT NOT NULL,
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    project     TEXT NOT NULL,
    directory   TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    summary     TEXT,
    PRIMARY KEY (team_id, id)
);
CREATE INDEX idx_sessions_project ON sessions(team_id, project);

-- ─── Observations ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS observations (
    id              BIGSERIAL PRIMARY KEY,
    team_id         UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL,
    type            TEXT NOT NULL,
    title           TEXT NOT NULL,
    content         TEXT NOT NULL,
    tool_name       TEXT,
    project         TEXT,
    scope           TEXT NOT NULL DEFAULT 'project',
    topic_key       TEXT,
    normalized_hash TEXT,
    revision_count  INTEGER NOT NULL DEFAULT 1,
    duplicate_count INTEGER NOT NULL DEFAULT 1,
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,

    -- Full-text search vector: auto-maintained via GENERATED STORED
    tsv             tsvector GENERATED ALWAYS AS (
                        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
                        setweight(to_tsvector('english', coalesce(content, '')), 'B') ||
                        setweight(to_tsvector('english', coalesce(type, '') || ' ' || coalesce(project, '')), 'C')
                    ) STORED
);

CREATE INDEX idx_obs_team_session  ON observations(team_id, session_id);
CREATE INDEX idx_obs_team_project  ON observations(team_id, project);
CREATE INDEX idx_obs_team_type     ON observations(team_id, type);
CREATE INDEX idx_obs_team_created  ON observations(team_id, created_at DESC);
CREATE INDEX idx_obs_team_topic    ON observations(team_id, topic_key, project, scope, updated_at DESC);
CREATE INDEX idx_obs_team_dedupe   ON observations(team_id, normalized_hash, project, scope, type, title);
CREATE INDEX idx_obs_team_deleted  ON observations(team_id, deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_obs_tsv           ON observations USING GIN(tsv);

-- ─── Prompts ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_prompts (
    id          BIGSERIAL PRIMARY KEY,
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL,
    content     TEXT NOT NULL,
    project     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_prompts_team_session ON user_prompts(team_id, session_id);
CREATE INDEX idx_prompts_team_project ON user_prompts(team_id, project);

-- ─── Sync Chunks (cloud-side tracking) ──────────────────────────────────
CREATE TABLE IF NOT EXISTS sync_chunks (
    chunk_id    TEXT NOT NULL,
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    created_by  TEXT NOT NULL DEFAULT '',
    sessions    INTEGER NOT NULL DEFAULT 0,
    memories    INTEGER NOT NULL DEFAULT 0,
    prompts     INTEGER NOT NULL DEFAULT 0,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, chunk_id)
);

-- ─── Refresh Tokens ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,                          -- SHA-256 hash of token
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_refresh_team ON refresh_tokens(team_id);
```

---

## Cloud Server Architecture

### Server Struct

```go
// Package cloudserver provides the HTTP API for Mnemo cloud mode.
package cloudserver

type CloudServer struct {
    store  *cloudstore.CloudStore
    auth   *auth.Service
    mux    *http.ServeMux
    port   int
    listen func(string, string) (net.Listener, error)
    serve  func(net.Listener, http.Handler) error
}
```

This mirrors the existing `server.Server` struct pattern (see `internal/server/server.go:23-29`).

### Middleware Stack

```
Request
  │
  ├── Recovery (panic -> 500)
  ├── RequestID (X-Request-ID header)
  ├── Logger (structured log: method, path, status, duration)
  ├── CORS (configurable origins for future web dashboard)
  ├── Auth (JWT or API key -> team_id in context)
  │     ├── Authorization: Bearer <jwt>  -> validate JWT, extract team_id
  │     └── Authorization: Bearer engr_* -> validate API key, extract team_id
  ├── TeamScope (inject team_id into all queries)
  └── Handler
```

The auth middleware sets `team_id` in the request context. All cloudstore methods accept `team_id` as a parameter, ensuring row-level tenant isolation.

### Route Registration

```go
func (s *CloudServer) routes() {
    // Health (no auth required)
    s.mux.HandleFunc("GET /health", s.handleHealth)

    // Auth routes (no auth required)
    s.mux.HandleFunc("POST /auth/login",   s.handleLogin)     // team_name + secret -> JWT pair
    s.mux.HandleFunc("POST /auth/refresh", s.handleRefresh)   // refresh_token -> new JWT pair
    s.mux.HandleFunc("POST /auth/apikey",  s.withAuth(s.handleCreateAPIKey))

    // Sync routes (auth required)
    s.mux.HandleFunc("POST /cloud/push",      s.withAuth(s.handlePush))
    s.mux.HandleFunc("GET  /cloud/pull",       s.withAuth(s.handlePull))
    s.mux.HandleFunc("GET  /cloud/manifest",   s.withAuth(s.handleManifest))

    // Cloud search (auth required)
    s.mux.HandleFunc("GET  /cloud/search",     s.withAuth(s.handleSearch))
    s.mux.HandleFunc("GET  /cloud/context",    s.withAuth(s.handleContext))

    // Cloud CRUD (auth required)
    s.mux.HandleFunc("GET  /cloud/observations/{id}",   s.withAuth(s.handleGetObservation))
    s.mux.HandleFunc("GET  /cloud/observations/recent",  s.withAuth(s.handleRecentObservations))
    s.mux.HandleFunc("GET  /cloud/sessions/recent",      s.withAuth(s.handleRecentSessions))
    s.mux.HandleFunc("GET  /cloud/stats",                s.withAuth(s.handleStats))
}
```

All `/cloud/*` routes are prefixed to avoid collision with local server routes if both are ever composed.

### `--mode cloud` Integration in main.go

```go
func cmdServe(cfg store.Config) {
    mode := "local"  // default
    // Parse --mode flag from os.Args
    // Parse MNEMO_MODE env var as fallback

    switch mode {
    case "local":
        // existing SQLite server (unchanged)
        s, err := storeNew(cfg)
        // ...
        srv := newHTTPServer(s, port)
        startHTTP(srv)

    case "cloud":
        // Postgres cloud server
        cloudCfg := cloud.ConfigFromEnv()
        cs, err := cloudstore.New(cloudCfg)
        // ...
        authSvc := auth.NewService(cs, cloudCfg.JWTSecret)
        srv := cloudserver.New(cs, authSvc, port)
        srv.Start()
    }
}
```

---

## Auth Design

### JWT Claims

```go
type Claims struct {
    TeamID   string   `json:"team_id"`
    TeamName string   `json:"team_name"`
    Scopes   []string `json:"scopes"`   // ["push", "pull", "search", "admin"]
    jwt.RegisteredClaims
}
```

### Token Lifecycle

```
Client                                          Cloud Server
  │                                                │
  │  POST /auth/login                              │
  │  { "team": "my-team", "secret": "..." }        │
  │ ─────────────────────────────────────────────> │
  │                                                │ validate team + secret
  │  { access_token, refresh_token, expires_in }   │ generate JWT pair
  │ <───────────────────────────────────────────── │
  │                                                │
  │  GET /cloud/search?q=auth                      │
  │  Authorization: Bearer <access_token>          │
  │ ─────────────────────────────────────────────> │
  │                                                │ validate JWT, extract team_id
  │  { results: [...] }                            │
  │ <───────────────────────────────────────────── │
  │                                                │
  │  ... 1 hour later, access_token expired ...    │
  │                                                │
  │  POST /auth/refresh                            │
  │  { "refresh_token": "..." }                    │
  │ ─────────────────────────────────────────────> │
  │                                                │ validate refresh token
  │  { access_token, refresh_token, expires_in }   │ rotate both tokens
  │ <───────────────────────────────────────────── │
```

- **Access token**: 1 hour expiry, signed with HMAC-SHA256
- **Refresh token**: 7 day expiry, stored as SHA-256 hash in `refresh_tokens` table
- **Rotation**: Every refresh invalidates the old refresh token and issues new pair (prevents replay)

### API Key Design

```
Format: engr_<32 random hex chars>
        ^      ^
        prefix  random bytes

Storage: bcrypt hash in api_keys table
Lookup:  prefix index for O(1) candidate lookup, then bcrypt.Compare

Example: engr_a3f8c1d2e4f6789012345678abcdef01
```

API keys are created via `POST /auth/apikey` (requires JWT auth). Each key has:
- A human name ("CI pipeline", "alan-laptop")
- Scopes (subset of: push, pull, search, admin)
- Optional expiry
- Last-used timestamp (updated on each use)

### Middleware Implementation

```go
func (s *CloudServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        token := extractBearerToken(r)
        if token == "" {
            jsonError(w, 401, "missing authorization header")
            return
        }

        var teamID string
        if strings.HasPrefix(token, "engr_") {
            // API key flow
            teamID, err = s.auth.ValidateAPIKey(token)
        } else {
            // JWT flow
            claims, err = s.auth.ValidateJWT(token)
            teamID = claims.TeamID
        }

        if err != nil {
            jsonError(w, 401, "invalid credentials")
            return
        }

        ctx := context.WithValue(r.Context(), teamIDKey, teamID)
        next(w, r.WithContext(ctx))
    }
}
```

---

## Sync Protocol Design

### Push Flow (Local -> Cloud)

```
Client (RemoteTransport)                    Cloud Server
  │                                            │
  │  1. Export from SQLite                     │
  │     syncer.Export() -> ChunkData           │
  │                                            │
  │  2. Serialize + gzip                       │
  │     json.Marshal(chunk) -> gzip            │
  │                                            │
  │  3. POST /cloud/push                       │
  │     Content-Type: application/gzip         │
  │     Content-Encoding: gzip                 │
  │     X-Chunk-ID: a3f8c1d2                   │
  │     X-Chunk-Created-By: alan               │
  │     Authorization: Bearer <token>          │
  │     Body: <gzipped JSONL>                  │
  │ ─────────────────────────────────────────> │
  │                                            │ 4. Decompress + parse ChunkData
  │                                            │ 5. Check if chunk_id already exists
  │                                            │    for this team_id (idempotent)
  │                                            │ 6. Import sessions, observations,
  │                                            │    prompts into Postgres
  │                                            │ 7. Record chunk in sync_chunks
  │                                            │
  │  { "status": "ok",                        │
  │    "chunk_id": "a3f8c1d2",                │
  │    "imported": { sessions: 2, obs: 15 }}  │
  │ <───────────────────────────────────────── │
  │                                            │
  │  8. Record chunk as synced locally         │
  │     store.RecordSyncedChunk("a3f8c1d2")   │
```

### Pull Flow (Cloud -> Local)

```
Client (RemoteTransport)                    Cloud Server
  │                                            │
  │  1. GET /cloud/manifest                    │
  │     Authorization: Bearer <token>          │
  │ ─────────────────────────────────────────> │
  │                                            │ 2. Query sync_chunks for team_id
  │  { chunks: [{id, created_by, ...}, ...] } │
  │ <───────────────────────────────────────── │
  │                                            │
  │  3. Diff against local known chunks        │
  │     unknownIDs = remote - local            │
  │                                            │
  │  4. For each unknown chunk:                │
  │     GET /cloud/pull?chunk_id=<id>          │
  │     Authorization: Bearer <token>          │
  │ ─────────────────────────────────────────> │
  │                                            │ 5. Re-export chunk data from
  │                                            │    Postgres (by chunk_id metadata)
  │                                            │    or serve stored chunk blob
  │  Body: <gzipped JSONL>                     │
  │ <───────────────────────────────────────── │
  │                                            │
  │  6. Decompress + parse ChunkData           │
  │  7. Import into local SQLite               │
  │     store.Import(exportData)               │
  │  8. Record chunk as synced locally         │
  │     store.RecordSyncedChunk(chunkID)       │
```

### Idempotency Guarantees

- **Push**: Cloud checks `(team_id, chunk_id)` in `sync_chunks` before importing. Duplicate pushes return 200 with `"already_imported": true`.
- **Pull**: Client checks local `sync_chunks` before requesting. Downloading the same chunk twice is a no-op because `store.Import` uses `INSERT OR IGNORE` for sessions and generates new IDs for observations.
- **Chunk IDs**: Content-addressed (SHA-256 of serialized JSON, first 8 hex chars). Same content always produces the same chunk ID.

---

## Transport Interface Design

```go
// Package sync — new file: transport.go

// Transport defines how chunks are read and written during sync.
// This is the abstraction that allows the same Syncer to work with
// both local filesystem (.mnemo/ directory) and remote cloud server.
type Transport interface {
    // ReadManifest returns the manifest (chunk index).
    // Returns an empty manifest if none exists yet.
    ReadManifest() (*Manifest, error)

    // WriteManifest persists the manifest.
    WriteManifest(m *Manifest) error

    // WriteChunk writes a compressed chunk to the transport.
    // chunkID is the content-addressed ID (8 hex chars).
    // data is the gzipped JSONL bytes.
    WriteChunk(chunkID string, data []byte, entry ChunkEntry) error

    // ReadChunk reads a compressed chunk from the transport.
    // Returns the raw gzipped bytes.
    ReadChunk(chunkID string) ([]byte, error)
}
```

### FileTransport (Extracted from Current Code)

```go
// FileTransport reads/writes chunks to the local filesystem.
// This is the existing behavior, extracted from Syncer methods.
type FileTransport struct {
    syncDir string // Path to .mnemo/ directory
}

func NewFileTransport(syncDir string) *FileTransport {
    return &FileTransport{syncDir: syncDir}
}

func (ft *FileTransport) ReadManifest() (*Manifest, error)              { /* existing readManifest logic */ }
func (ft *FileTransport) WriteManifest(m *Manifest) error               { /* existing writeManifest logic */ }
func (ft *FileTransport) WriteChunk(id string, data []byte, _ ChunkEntry) error { /* existing writeGzip logic */ }
func (ft *FileTransport) ReadChunk(id string) ([]byte, error)           { /* existing readGzip logic */ }
```

### RemoteTransport (New)

```go
// Package remote
// RemoteTransport pushes/pulls chunks over HTTP to an Mnemo cloud server.
type RemoteTransport struct {
    baseURL    string       // e.g. "https://mnemo.example.com"
    token      string       // JWT or API key
    httpClient *http.Client
}

func NewRemoteTransport(baseURL, token string) *RemoteTransport {
    return &RemoteTransport{
        baseURL:    strings.TrimRight(baseURL, "/"),
        token:      token,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}

func (rt *RemoteTransport) ReadManifest() (*Manifest, error) {
    // GET /cloud/manifest with auth header
}

func (rt *RemoteTransport) WriteManifest(m *Manifest) error {
    // No-op for remote: cloud manages its own manifest
    return nil
}

func (rt *RemoteTransport) WriteChunk(id string, data []byte, entry ChunkEntry) error {
    // POST /cloud/push with gzipped body and chunk metadata headers
}

func (rt *RemoteTransport) ReadChunk(id string) ([]byte, error) {
    // GET /cloud/pull?chunk_id=<id> with auth header
}
```

### Refactored Syncer

```go
// Syncer keeps the original local constructor and adds an explicit custom-transport path.
type Syncer struct {
    store     *store.Store
    syncDir   string
    transport Transport
}

func New(s *store.Store, syncDir string) *Syncer {
    return &Syncer{store: s, syncDir: syncDir, transport: NewFileTransport(syncDir)}
}

// NewLocal is a readability alias for the default filesystem-backed constructor.
func NewLocal(s *store.Store, syncDir string) *Syncer {
    return New(s, syncDir)
}

func NewWithTransport(s *store.Store, transport Transport) *Syncer {
    return &Syncer{store: s, transport: transport}
}
```

**Implementation note**: The shipped code intentionally preserved `New(s, syncDir)` to avoid churn across existing tests and local sync call sites, then added `NewLocal` as a clearer alias plus `NewWithTransport` for remote/cloud sync. This differs from the earlier design draft but preserves the same adapter boundary.

---

## Config Design

### Environment Variables (Cloud Mode)

| Variable | Description | Default |
|----------|-------------|---------|
| `MNEMO_DATABASE_URL` | Postgres connection string for `mnemo cloud serve` | (required) |
| `MNEMO_JWT_SECRET` | HMAC secret for JWT signing (min 32 chars) | (required) |
| `MNEMO_CLOUD_CORS_ORIGINS` | Comma-separated allowed origins | `*` |
| `MNEMO_PORT` | HTTP server port | `7437` |
| `MNEMO_DATA_DIR` | Local SQLite data directory | `~/.mnemo` |

### Cloud Config Struct

```go
// internal/cloud/config.go
type Config struct {
    DSN            string   // Postgres connection string
    JWTSecret      string   // HMAC-SHA256 signing key
    CORSOrigins    []string // Allowed CORS origins
    MaxPoolConns   int      // Postgres connection pool max (default: 25)
    MigrateOnStart bool     // Run DDL on startup (default: true)
}

func ConfigFromEnv() Config {
    return Config{
        DSN:            os.Getenv("MNEMO_DATABASE_URL"),
        JWTSecret:      os.Getenv("MNEMO_JWT_SECRET"),
        CORSOrigins:    strings.Split(os.Getenv("MNEMO_CLOUD_CORS_ORIGINS"), ","),
        MaxPoolConns:   envInt("MNEMO_CLOUD_MAX_POOL", 25),
        MigrateOnStart: true,
    }
}
```

### Client Config (for RemoteTransport)

```go
// Stored in ~/.mnemo/cloud.json
{
    "server":  "https://mnemo.example.com",
    "token":   "engr_a3f8c1d2...",        // API key
    "team":    "my-team"
}
```

Created by `mnemo cloud login` (future CLI command, not in this phase).

---

## Error Handling

### Retry Strategy (RemoteTransport)

```go
// Exponential backoff with jitter for transient failures.
// Max 3 retries for: 429, 500, 502, 503, 504.
// No retry for: 400, 401, 403, 404, 409.

func (rt *RemoteTransport) doWithRetry(req *http.Request) (*http.Response, error) {
    maxRetries := 3
    baseDelay := 500 * time.Millisecond

    for attempt := 0; attempt <= maxRetries; attempt++ {
        resp, err := rt.httpClient.Do(req)
        if err != nil {
            // Network error: retry
            if attempt < maxRetries {
                sleep(baseDelay * (1 << attempt) + jitter())
                continue
            }
            return nil, fmt.Errorf("cloud: request failed after %d retries: %w", maxRetries, err)
        }

        if resp.StatusCode < 500 && resp.StatusCode != 429 {
            return resp, nil // Client error or success: don't retry
        }

        // Server error or rate limit: retry
        resp.Body.Close()
        if attempt < maxRetries {
            sleep(baseDelay * (1 << attempt) + jitter())
        }
    }
    return nil, fmt.Errorf("cloud: server error after %d retries", maxRetries)
}
```

### Idempotency

- **Push**: Chunk ID is content-addressed. Pushing the same chunk twice is always safe -- the server checks `(team_id, chunk_id)` uniqueness and returns success.
- **Pull**: Client tracks imported chunks locally. Re-importing the same chunk is a no-op via `INSERT OR IGNORE` on sessions and new IDs for observations.
- **Auth**: Token refresh is NOT idempotent. The old refresh token is revoked on use. If a refresh request fails mid-flight, the client must re-login.

### Partial Failure (Multi-Chunk Pull)

```go
// Pull imports chunks one at a time. If chunk N fails:
// - Chunks 1..N-1 are already committed to local SQLite
// - Chunk N is NOT recorded as synced
// - Next pull will retry from chunk N
// This is safe because import uses INSERT OR IGNORE for sessions.
```

---

## Security

### HTTPS

- Cloud mode SHOULD be deployed behind a TLS-terminating reverse proxy (nginx, Caddy, cloud load balancer).
- The Go server itself listens on plain HTTP (matching the existing `server.Start()` pattern).
- `RemoteTransport` validates TLS certificates by default (`http.Client` default behavior).

### Token Storage

- **JWT secret**: Environment variable only. NEVER logged, NEVER in config files.
- **API keys**: Raw key shown once at creation. Stored as bcrypt hash in Postgres. Client stores in `~/.mnemo/cloud.json` with file permissions `0600`.
- **Refresh tokens**: Stored as SHA-256 hash in Postgres. Raw token sent to client only in login/refresh responses.

### Credential Management

- API keys use the `engr_` prefix for easy identification in logs and leak detection (similar to `sk-` for OpenAI keys).
- API keys can be scoped (push-only, pull-only, search-only) for principle of least privilege.
- API keys have optional expiry. Expired keys are rejected at validation time.
- Refresh tokens are single-use (rotation on every refresh). Stolen refresh tokens become invalid after one use.

### Multi-Tenant Isolation

- Every Postgres query includes `WHERE team_id = $N`.
- The `team_id` comes from the auth middleware (extracted from JWT claims or API key lookup). It is NEVER taken from request parameters.
- There is no admin/superuser route that bypasses team scoping (by design).

---

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit | JWT generation, validation, expiry | `internal/cloud/auth/auth_test.go` -- pure Go tests, no DB |
| Unit | API key generation, bcrypt, prefix extraction | `internal/cloud/auth/auth_test.go` -- pure Go tests |
| Unit | Transport interface compliance | `internal/sync/transport_test.go` -- FileTransport with temp dirs |
| Unit | RemoteTransport HTTP calls | `internal/cloud/remote/transport_test.go` -- `httptest.Server` |
| Integration | CloudStore CRUD, search, FTS | `internal/cloud/cloudstore/cloudstore_test.go` -- `ory/dockertest` spinning real Postgres |
| Integration | CloudServer routes, middleware, push/pull | `internal/cloud/cloudserver/cloudserver_test.go` -- `httptest.Server` + dockertest Postgres |
| Integration | End-to-end sync: local SQLite -> cloud Postgres -> local SQLite | Separate test file composing Syncer + RemoteTransport + httptest cloud server |
| Existing | Ensure zero regression on SQLite store | Existing `store_test.go`, `sync_test.go`, `server_test.go` MUST pass unchanged |

### Test Seam Pattern

Following the existing codebase convention (var-level function injection for test seams):

```go
// cloudstore.go
var openDB = sql.Open  // Same pattern as internal/store/store.go:23
```

### dockertest Example

```go
func TestCloudStoreSearch(t *testing.T) {
    pool, err := dockertest.NewPool("")
    require.NoError(t, err)

    resource, err := pool.Run("postgres", "16-alpine", []string{
        "POSTGRES_PASSWORD=test",
        "POSTGRES_DB=mnemo_test",
    })
    require.NoError(t, err)
    defer pool.Purge(resource)

    dsn := fmt.Sprintf("postgres://postgres:test@localhost:%s/mnemo_test?sslmode=disable",
        resource.GetPort("5432/tcp"))

    // Wait for Postgres to be ready
    pool.Retry(func() error {
        db, err := sql.Open("postgres", dsn)
        if err != nil { return err }
        return db.Ping()
    })

    cs, err := cloudstore.New(cloud.Config{DSN: dsn})
    require.NoError(t, err)
    defer cs.Close()

    // Test search with tsvector...
}
```

---

## Dependency Diagram

```
cmd/mnemo/main.go
    │
    ├── internal/store        (SQLite, unchanged)
    ├── internal/server       (local HTTP, unchanged)
    ├── internal/mcp          (MCP stdio, unchanged)
    ├── internal/sync         (MODIFIED: Transport interface)
    │       ├── FileTransport    (extracted from existing code)
    │       └── Transport        (new interface)
    │
    └── internal/cloud/       (NEW: entire tree)
            ├── cloudstore    (Postgres storage)
            │       └── depends on: lib/pq, database/sql
            ├── cloudserver   (cloud HTTP routes)
            │       └── depends on: cloudstore, auth, net/http
            ├── auth          (JWT + API key)
            │       └── depends on: golang.org/x/crypto, cloudstore (for key lookup)
            └── remote        (RemoteTransport)
                    └── depends on: sync.Transport (interface), net/http
```

**No circular dependencies**. The dependency flow is strictly:
- `main.go` -> `cloud/*` -> `cloudstore` (no reverse)
- `remote` -> `sync.Transport` (interface only, no concrete dependency on sync package internals)
- `cloudserver` -> `cloudstore` + `auth` (no reverse)

---

## Migration / Rollout

### Phase 1: Transport Refactor (Non-Breaking)

Refactor `internal/sync/` to use the Transport interface. `NewLocal()` wrapper maintains backwards compatibility. The CLI `mnemo sync` command continues to work identically.

### Phase 2: Cloud Infrastructure

Add `internal/cloud/` package tree. The cloud server is opt-in via `--mode cloud`. Local mode is completely unaffected.

### Phase 3: Remote Transport + CLI

Add `mnemo sync --remote` flag and `RemoteTransport`. Users can push/pull to cloud by configuring `~/.mnemo/cloud.json`.

### Rollback Plan

- Phase 1 rollback: Revert transport extraction, restore inline filesystem code in Syncer. Single commit revert.
- Phase 2 rollback: Remove `internal/cloud/` directory. No other packages depend on it. Single `rm -rf` + revert main.go changes.
- Phase 3 rollback: Remove RemoteTransport and `--remote` flag. FileTransport continues working.

Each phase is independently deployable and revertable because cloud packages are strictly additive.

---

## Open Questions

- [ ] **Team provisioning**: How are teams created initially? Self-service signup or admin CLI command? For MVP, likely `mnemo cloud create-team <name>` CLI command that inserts directly into Postgres.
- [ ] **Conflict resolution on pull**: If two developers save observations with the same `topic_key`, who wins? Current proposal: last-write-wins (same as local SQLite behavior with `topic_key` upserts). May need CRDTs or manual merge in the future.
- [ ] **Chunk storage on cloud**: Should the cloud server store the raw gzipped chunk blobs (for fast re-serve on pull), or re-export from Postgres on each pull? Storing blobs is faster but doubles storage. Re-exporting is slower but eliminates blob management. Recommendation: store blobs in a `chunk_blobs` table for v1, revisit if storage becomes an issue.
- [ ] **Rate limiting**: Should the cloud server rate-limit push/pull operations? Not critical for v1 (teams are small), but should be planned for the middleware stack.
