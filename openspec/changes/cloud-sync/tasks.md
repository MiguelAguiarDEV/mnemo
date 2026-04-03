# Tasks: Cloud Sync

## Phase 1: Dependencies & Project Setup

- [x] 1.1 Add `github.com/lib/pq` and `golang.org/x/crypto` to `go.mod` via `go get` (Covers: NFR-02) [S]
- [x] 1.2 Add `github.com/golang-jwt/jwt/v5` to `go.mod` via `go get` (Covers: AUTH-06) [S]
- [x] 1.3 Add `github.com/ory/dockertest/v3` as a test dependency in `go.mod` (Covers: NFR-01) [S]
- [x] 1.4 Create directory tree: `internal/cloud/cloudstore/`, `internal/cloud/cloudserver/`, `internal/cloud/auth/`, `internal/cloud/remote/` (Covers: infrastructure) [S]
- [x] 1.5 Create `internal/cloud/config.go` with `Config` struct and `ConfigFromEnv()` function reading `MNEMO_CLOUD_DSN`, `MNEMO_CLOUD_JWT_SECRET`, `MNEMO_CLOUD_CORS_ORIGINS`, `MNEMO_CLOUD_MAX_POOL` env vars (Covers: CLOUD-SRV-01) [S]
- [x] 1.6 Create `docker-compose.yml` at project root with Postgres 16-alpine service for local dev/testing (port 5433 to avoid conflicts) (Covers: NFR-04) [S]

**Phase 1 test task:**

- [x] 1.7 Verify `go build ./...` succeeds with new dependencies; verify `go test ./...` passes (existing tests unbroken) (Covers: NFR-03) [S]

---

## Phase 2: Postgres Schema & CloudStore Foundation

**Dependencies: Phase 1 complete**

- [x] 2.1 Create `internal/cloud/cloudstore/schema.go` with DDL constants for all tables: `cloud_users`, `cloud_sessions`, `cloud_observations`, `cloud_prompts`, `cloud_chunks`, `cloud_sync_chunks`. Use `CREATE TABLE IF NOT EXISTS`. Include all indexes from the spec. Include tsvector trigger functions for `cloud_observations` and `cloud_prompts`. (Covers: PG-STORE-01, PG-STORE-02, PG-STORE-03, PG-STORE-04, PG-STORE-05, PG-STORE-06, FTS-01, FTS-02) [L]
- [x] 2.2 Create `internal/cloud/cloudstore/cloudstore.go` with `CloudStore` struct holding `*sql.DB`, a `New(cfg cloud.Config) (*CloudStore, error)` constructor that opens a Postgres connection via `lib/pq`, runs schema initialization within a single transaction, and exposes a `Close()` method. Use `var openDB = sql.Open` test seam pattern matching `internal/store/store.go:23`. (Covers: PG-STORE-01, CLOUD-SRV-01, NFR-04) [M]
- [x] 2.3 Add user CRUD methods to `CloudStore`: `CreateUser(username, email, password string) (*CloudUser, error)` (bcrypt hash password, cost >= 10), `GetUserByUsername(username string) (*CloudUser, error)`, `GetUserByAPIKeyHash(hash string) (*CloudUser, error)`. Define `CloudUser` struct with fields matching `cloud_users` schema. (Covers: PG-STORE-06, AUTH-01, NFR-02) [M]
- [x] 2.4 Add session methods to `CloudStore`: `CreateSession(userID, sessionID, project, directory string) error`, `EndSession(sessionID, summary string) error`, `RecentSessions(userID, project string, limit int) ([]CloudSession, error)`. All queries MUST include `WHERE user_id = $N`. Define `CloudSession` struct. (Covers: PG-STORE-02, PG-STORE-07) [M]
- [x] 2.5 Add observation methods to `CloudStore`: `AddObservation(userID string, params AddCloudObservationParams) (int64, error)`, `GetObservation(userID string, id int64) (*CloudObservation, error)`, `RecentObservations(userID, project, scope string, limit int) ([]CloudObservation, error)`, `DeleteObservation(userID string, id int64, hard bool) error`. All queries MUST filter by `user_id`. Define `CloudObservation` and `AddCloudObservationParams` structs. (Covers: PG-STORE-03, PG-STORE-07) [M]
- [x] 2.6 Add prompt methods to `CloudStore`: `AddPrompt(userID string, params AddCloudPromptParams) (int64, error)`, `RecentPrompts(userID, project string, limit int) ([]CloudPrompt, error)`. All queries MUST filter by `user_id`. Define `CloudPrompt` and `AddCloudPromptParams` structs. (Covers: PG-STORE-04, PG-STORE-07) [S]
- [x] 2.7 Add chunk methods to `CloudStore`: `StoreChunk(userID, chunkID, createdBy string, data []byte, sessions, memories, prompts int) error` (uses `INSERT ... ON CONFLICT DO NOTHING` for idempotency), `GetChunk(userID, chunkID string) ([]byte, error)`, `ListChunks(userID string) ([]CloudChunkEntry, error)`, `RecordSyncedChunk(userID, chunkID string) error`, `GetSyncedChunks(userID string) (map[string]bool, error)`. Define `CloudChunkEntry` struct. (Covers: PG-STORE-05) [M]
- [x] 2.8 Add stats/context methods to `CloudStore`: `Stats(userID string) (*CloudStats, error)`, `FormatContext(userID, project, scope string) (string, error)`. Context formatting MUST match `internal/store/store.go` output format. (Covers: CLOUD-SRV-07) [M]

**Phase 2 test task:**

- [x] 2.9 Create `internal/cloud/cloudstore/cloudstore_test.go` with `ory/dockertest` spinning a real Postgres 16-alpine container. Test: schema idempotent creation (run twice, no error), user CRUD (create, duplicate username rejection, duplicate email rejection), session lifecycle (create, end with summary), observation CRUD (add, get, soft-delete, verify deleted_at filter), prompt CRUD, chunk idempotency (store same chunk twice, verify no duplicate), data isolation (user A cannot see user B's observations). Must cover scenarios from PG-STORE-01 through PG-STORE-07. (Covers: PG-STORE-01 through PG-STORE-07) [L]

---

## Phase 3: Full-Text Search (tsvector)

**Dependencies: Phase 2 tasks 2.1-2.5 complete**

- [x] 3.1 Create `internal/cloud/cloudstore/search.go` with `Search(userID, query string, opts CloudSearchOptions) ([]CloudSearchResult, error)` using `plainto_tsquery('english', $1)` and `ts_rank_cd(search_vector, query)` for ranking. Filter by `user_id`, optional `type`, `project`, `scope`, `limit`. Define `CloudSearchOptions` and `CloudSearchResult` structs. (Covers: CLOUD-SRV-06, FTS-01, NFR-01) [M]
- [x] 3.2 Add query parsing logic in `search.go`: convert user input to safe tsquery (strip special chars, handle multi-word with `&`, support prefix matching with `:*`). Must not produce SQL injection via string concatenation -- use parameterized queries only. (Covers: FTS-03, NFR-02) [M]
- [x] 3.3 Add `SearchPrompts(userID, query, project string, limit int) ([]CloudPrompt, error)` to `search.go` using the `cloud_prompts.search_vector` column with `plainto_tsquery`. (Covers: FTS-02) [S]

**Phase 3 test task:**

- [x] 3.4 Add search tests to `cloudstore_test.go` (dockertest): verify title matches rank higher than content matches (FTS-01 scenario), multi-term queries return combined matches first, prefix matching works, special characters in queries don't cause SQL errors, search filters by user_id (user A's results don't include user B's observations), empty results return empty array not error, prompt search returns user-scoped results. (Covers: FTS-01, FTS-02, FTS-03, NFR-01) [M]

---

## Phase 4: JWT Authentication

**Dependencies: Phase 2 task 2.3 complete (user CRUD)**

- [x] 4.1 Create `internal/cloud/auth/auth.go` with `Service` struct holding JWT secret ([]byte) and a reference to cloudstore for user lookups. Constructor: `NewService(store *cloudstore.CloudStore, jwtSecret string) (*Service, error)` -- MUST validate secret >= 32 bytes. (Covers: AUTH-06, NFR-02) [S]
- [x] 4.2 Add JWT generation to `auth.go`: `GenerateTokenPair(userID, username string) (accessToken, refreshToken string, err error)`. Access token: HMAC-SHA256, claims {sub: userID, username, type: "access", iat, exp: 1h}. Refresh token: claims {sub: userID, type: "refresh", iat, exp: 7d}. (Covers: AUTH-02, AUTH-06) [M]
- [x] 4.3 Add JWT validation to `auth.go`: `ValidateAccessToken(tokenStr string) (*Claims, error)` -- parse, validate signature, check expiry, verify `type == "access"`. Define `Claims` struct with `UserID`, `Username`, `Type` fields plus `jwt.RegisteredClaims`. (Covers: AUTH-05, AUTH-06) [M]
- [x] 4.4 Add token refresh to `auth.go`: `RefreshAccessToken(refreshTokenStr string) (newAccessToken string, err error)` -- validate refresh token, verify `type == "refresh"`, extract userID, generate new access token. (Covers: AUTH-03) [S]
- [x] 4.5 Add user registration to `auth.go`: `Register(username, email, password string) (*AuthResult, error)` -- validate password length >= 8, call `store.CreateUser`, generate token pair, return `AuthResult{UserID, Username, AccessToken, RefreshToken, ExpiresIn}`. (Covers: AUTH-01) [M]
- [x] 4.6 Add user login to `auth.go`: `Login(username, password string) (*AuthResult, error)` -- look up user by username, `bcrypt.CompareHashAndPassword`, generate token pair. On failure, return generic "invalid credentials" (no username disclosure). Use constant-time comparison. (Covers: AUTH-02) [M]
- [x] 4.7 Create `internal/cloud/auth/apikey.go` with API key functions: `GenerateAPIKey() (plainKey string, hash string, err error)` -- generate 32 random hex bytes prefixed with `eng_`, return plain key and SHA-256 hash. `ValidateAPIKey(store, providedKey string) (*CloudUser, error)` -- hash the provided key with SHA-256, look up user by `api_key_hash`. (Covers: AUTH-04, NFR-02) [M]

**Phase 4 test task:**

- [x] 4.8 Create `internal/cloud/auth/auth_test.go`: test JWT generation (valid claims, correct expiry), validation (valid token passes, expired token fails, wrong type fails, tampered signature fails), refresh (valid refresh gives new access, expired refresh fails, access token as refresh fails), registration (success with token pair, duplicate username returns error, weak password rejected), login (correct password succeeds, wrong password returns generic error, non-existent user returns same error as wrong password), API key (generation produces `eng_` prefix, hash lookup finds correct user, revoked key fails). Pure Go tests for auth logic; dockertest for store-dependent tests. (Covers: AUTH-01 through AUTH-06) [L]

---

## Phase 5: Cloud Server HTTP Layer

**Dependencies: Phase 2, Phase 3, Phase 4 complete**

- [x] 5.1 Create `internal/cloud/cloudserver/cloudserver.go` with `CloudServer` struct holding `*cloudstore.CloudStore`, `*auth.Service`, `*http.ServeMux`, `port int`. Constructor: `New(store, authSvc, port) *CloudServer`. Add `Start() error` method matching `internal/server/server.go` pattern (use `var` test seams for listen/serve). Register all routes. (Covers: CLOUD-SRV-01, CLOUD-SRV-02) [M]
- [x] 5.2 Create `internal/cloud/cloudserver/middleware.go` with `withAuth(next http.HandlerFunc) http.HandlerFunc` -- extract `Authorization: Bearer <token>` header, detect `eng_` prefix for API key flow vs JWT flow, on success inject `userID` into request context via `context.WithValue`, on failure return 401 JSON error. Add `getUserID(r *http.Request) string` context helper. (Covers: AUTH-05, CLOUD-SRV-03) [M]
- [x] 5.3 Add health endpoint handler: `handleHealth` -- return `{"status":"ok","service":"mnemo-cloud","version":"0.1.0"}`. No auth required. (Covers: CLOUD-SRV-02) [S]
- [x] 5.4 Add auth route handlers in `cloudserver.go`: `handleRegister` (POST /auth/register -- parse JSON body, call `authSvc.Register`, return 201 with tokens), `handleLogin` (POST /auth/login -- parse body, call `authSvc.Login`, return 200 with tokens), `handleRefresh` (POST /auth/refresh -- parse body, call `authSvc.RefreshAccessToken`, return 200). (Covers: CLOUD-SRV-03) [M]
- [x] 5.5 Add API key route handlers: `handleGenerateAPIKey` (POST /auth/api-key, auth required -- generate key, store hash, return 201 with plain key), `handleRevokeAPIKey` (DELETE /auth/api-key, auth required -- set api_key_hash to NULL, return 200). (Covers: CLOUD-SRV-08) [M]
- [x] 5.6 Create `internal/cloud/cloudserver/push_pull.go` with push handler: `handlePush` (POST /sync/push, auth required) -- parse JSON body with `chunk_id`, `created_by`, `data` fields; validate chunk_id format (8 hex chars); decompose `data` into sessions/observations/prompts and insert into respective tables via `cloudstore`; store raw chunk in `cloud_chunks`; return 200 with counts. Body limit 50MB via `http.MaxBytesReader`. (Covers: CLOUD-SRV-04, ERR-07, ERR-08) [L]
- [x] 5.7 Add pull handlers to `push_pull.go`: `handlePullManifest` (GET /sync/pull -- return user's chunk manifest as JSON), `handlePullChunk` (GET /sync/pull/{chunk_id} -- return chunk data JSONB, 404 if not found or wrong user). (Covers: CLOUD-SRV-05) [M]
- [x] 5.8 Add search handler: `handleSearch` (GET /sync/search, auth required) -- parse `q`, `type`, `project`, `scope`, `limit` query params; call `cloudstore.Search`; return results as JSON array. Return 400 if `q` is missing. (Covers: CLOUD-SRV-06) [S]
- [x] 5.9 Add context handler: `handleContext` (GET /sync/context, auth required) -- parse `project`, `scope` query params; call `cloudstore.FormatContext`; return `{"context": "..."}`. (Covers: CLOUD-SRV-07) [S]
- [x] 5.10 Add error handling: ensure all handler errors return standard `{"error":"message"}` JSON format. Database connection errors return 503. Invalid JSON returns 400. Auth failures return 401. (Covers: ERR-05, ERR-06, ERR-08, NFR-04) [S]

**Phase 5 test task:**

- [x] 5.11 Create `internal/cloud/cloudserver/cloudserver_test.go` using `httptest.Server` + dockertest Postgres: test health endpoint (no auth, returns 200), register + login flow (register returns tokens, login with same creds succeeds, login with wrong password returns 401), auth middleware (missing header returns 401, expired JWT returns 401, valid JWT passes, valid API key passes), push endpoint (valid chunk returns 200 with counts, duplicate chunk is idempotent, missing auth returns 401, oversized body returns 413, invalid JSON returns 400), pull manifest (returns correct chunk count, empty for new user), pull chunk (returns data, 404 for wrong user's chunk), search (returns ranked results, empty query returns 400, no results returns empty array), context (returns formatted string). (Covers: CLOUD-SRV-01 through CLOUD-SRV-08, ERR-05 through ERR-08) [L]

---

## Phase 6: Transport Interface & Remote Transport

**Dependencies: Phase 5 complete (cloud server functional for integration testing)**

- [x] 6.1 Create `internal/sync/transport.go` with `Transport` interface: `ReadManifest() (*Manifest, error)`, `WriteManifest(m *Manifest) error`, `WriteChunk(chunkID string, data []byte, entry ChunkEntry) error`, `ReadChunk(chunkID string) ([]byte, error)`. Implement `FileTransport` struct extracting existing filesystem logic from `sync.go` methods (`readManifest`, `writeManifest`, `writeGzip`, `readGzip`). (Covers: SYNC-05, NFR-05) [M]
- [x] 6.2 Refactor `internal/sync/sync.go`: replace `syncDir string` field in `Syncer` with `transport Transport`. Rename current `New(s, syncDir)` to `NewLocal(s, syncDir)` which creates a `FileTransport` internally. Add `New(s, transport)` that accepts any Transport. Update `Export` and `Import` methods to use `sy.transport.ReadManifest()`, `sy.transport.WriteManifest()`, `sy.transport.WriteChunk()`, `sy.transport.ReadChunk()` instead of direct filesystem calls. (Covers: SYNC-04, NFR-03, NFR-05) [M]
- [x] 6.3 Update `cmd/mnemo/main.go` line 570: change `mnemosync.New(s, syncDir)` to `mnemosync.NewLocal(s, syncDir)` to maintain backwards compatibility. (Covers: NFR-03) [S]
- [x] 6.4 Create `internal/cloud/remote/transport.go` with `RemoteTransport` struct implementing `sync.Transport`. Fields: `baseURL string`, `token string`, `httpClient *http.Client` (30s timeout). `ReadManifest()` calls `GET {baseURL}/sync/pull` with `Authorization: Bearer {token}`. `WriteManifest()` is a no-op (cloud manages its own manifest). `WriteChunk()` calls `POST {baseURL}/sync/push` with JSON body. `ReadChunk()` calls `GET {baseURL}/sync/pull/{chunkID}`. Add retry logic with exponential backoff (3 retries, 500ms base) for 429/5xx errors. (Covers: SYNC-01, SYNC-02, SYNC-03, SYNC-05, ERR-01, ERR-02, ERR-03) [L]

**Phase 6 test task:**

- [x] 6.5 Create `internal/sync/transport_test.go`: test `FileTransport` with temp directories (read/write manifest, read/write chunks, read non-existent manifest returns empty). Verify existing `sync_test.go` tests pass WITHOUT modification (NFR-03 regression check). (Covers: NFR-03, NFR-05) [M]
- [x] 6.6 Create `internal/cloud/remote/transport_test.go` using `httptest.Server`: test `RemoteTransport.ReadManifest()` returns parsed manifest, `WriteChunk()` sends correct POST with auth header, `ReadChunk()` returns data from GET, retry logic triggers on 500 responses and eventually succeeds, 401 errors are NOT retried, network error returns descriptive error. (Covers: SYNC-01, SYNC-02, SYNC-03, ERR-01, ERR-02) [M]

---

## Phase 7: CLI Integration

**Dependencies: Phase 5, Phase 6 complete**

- [x] 7.1 Add `mnemo cloud` subcommand dispatch to `cmd/mnemo/main.go`: add `case "cloud":` in the main switch that calls `cmdCloud(cfg)`. `cmdCloud` dispatches on `os.Args[2]`: `serve`, `register`, `login`, `sync`, `status`, `api-key`. (Covers: CLI-02, CLI-04) [M]
- [x] 7.2 Implement `cmdCloudServe` in `cmd/mnemo/main.go`: parse `--port` flag (default 8080) and `--database-url` flag (falls back to `MNEMO_DATABASE_URL`), parse `MNEMO_JWT_SECRET`. Validate required vars present. Create `cloudstore.New()`, `auth.NewService()`, `cloudserver.New()`, call `Start()`. Exit with error if `DATABASE_URL` or `JWT_SECRET` missing. (Covers: CLI-04, CLOUD-SRV-01) [M]
- [x] 7.3 Create cloud config file support: add `loadCloudConfig()` function reading `~/.mnemo/cloud.json` (fields: `server_url`, `token`, `user_id`, `username`). Add `saveCloudConfig()` writing with `0600` permissions. Config precedence: CLI flags > env vars > config file. (Covers: CLI-03) [M]
- [x] 7.4 Implement `cmdCloudRegister`: parse `--server` flag (required), prompt for username/email/password interactively, call `POST /auth/register` on the server, save credentials to `~/.mnemo/cloud.json`. (Covers: CLI-02) [M]
- [x] 7.5 Implement `cmdCloudLogin`: parse `--server` flag, prompt for username/password, call `POST /auth/login`, save credentials to config file. (Covers: CLI-02) [S]
- [x] 7.6 Implement `cmdCloudSync`: load cloud config, create `RemoteTransport` with server_url and token, create `Syncer` with remote transport, call `Export` (push) then create remote syncer for `Import` (pull). Print summary of pushed/pulled chunks. (Covers: CLI-02, SYNC-04) [M]
- [x] 7.7 Implement `cmdCloudStatus`: load cloud config, call `GET /sync/pull` to get manifest, compare with local synced chunks, print status (local chunks, remote chunks, pending import/export). (Covers: CLI-02) [S]
- [x] 7.8 Implement `cmdCloudAPIKey`: load cloud config, call `POST /auth/api-key`, display generated key once with warning to save it. (Covers: CLI-02, CLOUD-SRV-08) [S]
- [x] 7.9 Add `--remote` and `--token` flags to existing `cmdSearch` and `cmdContext`: when `--remote` is provided, create an HTTP client and query the cloud server's `/sync/search` or `/sync/context` endpoints instead of local SQLite. Fall back to env vars `MNEMO_REMOTE_URL` and `MNEMO_TOKEN`. (Covers: CLI-01) [M]
- [x] 7.10 Update `printUsage()` in `cmd/mnemo/main.go` to include cloud subcommands and `--remote`/`--token` flags documentation. (Covers: CLI-01, CLI-02) [S]

**Phase 7 test task:**

- [x] 7.11 Add CLI tests to `cmd/mnemo/main_test.go`: test `cmdCloudServe` fails with missing DATABASE_URL (CLOUD-SRV-01 scenario), test cloud config file load/save with correct permissions, test `--remote` flag on search dispatches to HTTP client, test default behavior (no --remote) still uses local SQLite. Verify ALL existing main_test.go tests still pass. (Covers: CLI-01 through CLI-04, NFR-03) [M]

---

## Phase 8: End-to-End Integration & Error Handling

**Dependencies: All previous phases complete**

- [x] 8.1 Create `internal/cloud/integration_test.go`: full round-trip test using dockertest Postgres + httptest cloud server + real SQLite local store. Flow: (1) save observations to local SQLite, (2) create RemoteTransport pointing to httptest server, (3) export + push local chunks, (4) verify chunks appear in Postgres, (5) create a second local SQLite store, (6) pull from cloud into second store, (7) verify observations match original. (Covers: SYNC-04, NFR-05, ERR-04) [L]
- [x] 8.2 Add error scenario tests: push with network failure (transport returns error, local chunk NOT marked synced), pull with network failure (partial import rolls back), token expiry mid-sync (401 handling), duplicate push idempotency, oversized chunk rejection (413). (Covers: ERR-01, ERR-02, ERR-03, ERR-04, ERR-07) [M]
- [x] 8.3 Add Postgres connection failure tests: cloud server returns 503 when DB is unreachable, health endpoint returns degraded status. (Covers: ERR-05, NFR-04) [S]
- [x] 8.4 Add rate limiting SHOULD tests (if implemented): verify login endpoint returns 429 after 10 rapid attempts. (Covers: ERR-06) [S]
- [x] 8.5 Run full regression: `go test ./... -cover -race`. Verify: (1) ALL existing tests in `internal/store/`, `internal/server/`, `internal/sync/`, `internal/mcp/`, `cmd/mnemo/` pass without modification, (2) new `internal/cloud/` packages have >= 80% coverage, (3) no race conditions detected. (Covers: NFR-03, NFR-01, NFR-02) [M]

---

## Phase 9: Cleanup & Documentation

**Dependencies: Phase 8 complete**

- [x] 9.1 Add doc comments to all exported types and functions in `internal/cloud/` packages following Go doc conventions. Ensure `go vet ./...` and `go lint ./...` pass clean. (Covers: cleanup) [M]
- [x] 9.2 Update `DOCS.md` or `README.md` with cloud sync section: environment variables, `mnemo cloud serve` usage, `mnemo cloud register/login/sync` workflow, docker-compose setup instructions, security notes (JWT secret management, HTTPS recommendation). (Covers: documentation) [M]
- [x] 9.3 Verify `go mod tidy` leaves no unused dependencies. Verify `go build ./...` produces a clean binary. Run `mnemo version` to confirm binary works. (Covers: NFR-03) [S]

---

## Requirement Coverage Matrix

| Requirement ID | Task(s) |
|----------------|---------|
| PG-STORE-01 | 2.1, 2.2, 2.9 |
| PG-STORE-02 | 2.1, 2.4, 2.9 |
| PG-STORE-03 | 2.1, 2.5, 2.9 |
| PG-STORE-04 | 2.1, 2.6, 2.9 |
| PG-STORE-05 | 2.1, 2.7, 2.9 |
| PG-STORE-06 | 2.1, 2.3, 2.9 |
| PG-STORE-07 | 2.4, 2.5, 2.6, 2.9 |
| AUTH-01 | 4.5, 4.8 |
| AUTH-02 | 4.2, 4.6, 4.8 |
| AUTH-03 | 4.4, 4.8 |
| AUTH-04 | 4.7, 4.8 |
| AUTH-05 | 4.3, 5.2, 4.8 |
| AUTH-06 | 1.2, 4.1, 4.2, 4.3, 4.8 |
| CLOUD-SRV-01 | 1.5, 2.2, 5.1, 7.2 |
| CLOUD-SRV-02 | 5.1, 5.3, 5.11 |
| CLOUD-SRV-03 | 5.2, 5.4, 5.11 |
| CLOUD-SRV-04 | 5.6, 5.11 |
| CLOUD-SRV-05 | 5.7, 5.11 |
| CLOUD-SRV-06 | 3.1, 5.8, 5.11 |
| CLOUD-SRV-07 | 2.8, 5.9, 5.11 |
| CLOUD-SRV-08 | 5.5, 7.8, 5.11 |
| SYNC-01 | 6.4, 6.6 |
| SYNC-02 | 6.4, 6.6 |
| SYNC-03 | 6.4, 6.6 |
| SYNC-04 | 6.2, 7.6, 8.1 |
| SYNC-05 | 6.1, 6.4, 8.1 |
| CLI-01 | 7.9, 7.10, 7.11 |
| CLI-02 | 7.1, 7.4, 7.5, 7.6, 7.7, 7.8, 7.10, 7.11 |
| CLI-03 | 7.3, 7.11 |
| CLI-04 | 7.1, 7.2, 7.11 |
| FTS-01 | 2.1, 3.1, 3.4 |
| FTS-02 | 2.1, 3.3, 3.4 |
| FTS-03 | 3.2, 3.4 |
| NFR-01 | 1.3, 3.1, 3.4, 8.5 |
| NFR-02 | 1.1, 2.3, 3.2, 4.1, 4.7, 8.5 |
| NFR-03 | 1.7, 6.2, 6.3, 6.5, 7.11, 8.5, 9.3 |
| NFR-04 | 1.6, 2.2, 5.10, 8.3 |
| NFR-05 | 6.1, 6.4, 8.1 |
| ERR-01 | 6.4, 8.2 |
| ERR-02 | 6.4, 8.2 |
| ERR-03 | 6.4, 8.2 |
| ERR-04 | 8.1, 8.2 |
| ERR-05 | 5.10, 8.3 |
| ERR-06 | 5.10, 8.4 |
| ERR-07 | 5.6, 8.2 |
| ERR-08 | 5.6, 5.10, 5.11 |

---

## Task Dependency Graph

```
Phase 1 (1.1-1.7)
    |
    v
Phase 2 (2.1-2.9)
    |
    +----------+----------+
    |                     |
    v                     v
Phase 3 (3.1-3.4)    Phase 4 (4.1-4.8)
    |                     |
    +----------+----------+
               |
               v
         Phase 5 (5.1-5.11)
               |
               v
         Phase 6 (6.1-6.6)
               |
               v
         Phase 7 (7.1-7.11)
               |
               v
         Phase 8 (8.1-8.5)
               |
               v
         Phase 9 (9.1-9.3)
```

**Note:** Phases 3 and 4 can run in PARALLEL (both depend only on Phase 2, not on each other).
