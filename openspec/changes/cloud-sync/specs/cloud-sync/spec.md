# Cloud Sync Specification

## Purpose

This specification defines the behavior for Mnemo's cloud synchronization feature: a self-hosted server backed by PostgreSQL that enables multi-device memory sync, team collaboration, and authenticated remote access. The cloud mode operates alongside the existing local SQLite mode without breaking any current functionality.

The feature introduces six new packages/domains and modifies one existing domain:

| Domain | Package | Status |
|--------|---------|--------|
| Postgres Store | `internal/cloud/pgstore` | NEW |
| JWT Authentication | `internal/cloud/auth` | NEW |
| Cloud Server | `internal/cloud/server` | NEW |
| Remote Sync Transport | `internal/cloud/sync` | NEW |
| CLI Integration | `cmd/mnemo` | MODIFIED |
| Cloud Full-Text Search | `internal/cloud/pgstore` (FTS) | NEW |

---

## Domain 1: Postgres Store (pgstore)

### Purpose

A PostgreSQL-backed implementation of the Mnemo storage layer that mirrors the local SQLite schema but uses PostgreSQL-native types (UUID, TIMESTAMPTZ, tsvector) for cloud deployments.

---

### PG-STORE-01: Schema Initialization

The system MUST initialize the PostgreSQL schema idempotently using `CREATE TABLE IF NOT EXISTS` for all tables. The system MUST NOT fail if the schema already exists. The system SHALL run all DDL within a single transaction to ensure atomicity.

#### Scenario: First-time schema creation on empty database

- GIVEN a PostgreSQL database with no Mnemo tables
- WHEN the pgstore is initialized
- THEN tables `cloud_users`, `cloud_sessions`, `cloud_observations`, `cloud_prompts`, `cloud_chunks`, and `cloud_sync_chunks` MUST be created
- AND all indexes and triggers MUST be created
- AND the function MUST return without error

#### Scenario: Idempotent re-initialization on existing schema

- GIVEN a PostgreSQL database with all Mnemo tables already present
- WHEN the pgstore is initialized again
- THEN the initialization MUST succeed without error
- AND existing data MUST NOT be modified or deleted

---

### PG-STORE-02: cloud_sessions Table

The system MUST store sessions in a `cloud_sessions` table with the following schema:

```sql
CREATE TABLE IF NOT EXISTS cloud_sessions (
    id          TEXT        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    project     TEXT        NOT NULL,
    directory   TEXT        NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    summary     TEXT
);

CREATE INDEX IF NOT EXISTS idx_cloud_sessions_user    ON cloud_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_cloud_sessions_project ON cloud_sessions(user_id, project);
```

The `user_id` column MUST reference `cloud_users(id)` with a foreign key constraint. The `started_at` column MUST default to the current timestamp. The `ended_at` column MAY be NULL for active sessions.

#### Scenario: Create a cloud session

- GIVEN an authenticated user with id "u-123"
- WHEN a session is created with id "sess-abc", project "mnemo", directory "/work/mnemo"
- THEN a row MUST be inserted into `cloud_sessions` with user_id "u-123"
- AND `started_at` MUST be set to the current timestamp
- AND `ended_at` MUST be NULL

#### Scenario: End a cloud session with summary

- GIVEN an active session "sess-abc" with ended_at NULL
- WHEN the session is ended with summary "Implemented auth module"
- THEN `ended_at` MUST be set to the current timestamp
- AND `summary` MUST be set to "Implemented auth module"

---

### PG-STORE-03: cloud_observations Table

The system MUST store observations in a `cloud_observations` table with the following schema:

```sql
CREATE TABLE IF NOT EXISTS cloud_observations (
    id              BIGSERIAL   PRIMARY KEY,
    session_id      TEXT        NOT NULL REFERENCES cloud_sessions(id),
    user_id         UUID        NOT NULL REFERENCES cloud_users(id),
    type            TEXT        NOT NULL,
    title           TEXT        NOT NULL,
    content         TEXT        NOT NULL,
    tool_name       TEXT,
    project         TEXT,
    scope           TEXT        NOT NULL DEFAULT 'project',
    topic_key       TEXT,
    normalized_hash TEXT,
    revision_count  INTEGER     NOT NULL DEFAULT 1,
    duplicate_count INTEGER     NOT NULL DEFAULT 1,
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_cloud_obs_session ON cloud_observations(session_id);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user    ON cloud_observations(user_id);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_type    ON cloud_observations(type);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_project ON cloud_observations(user_id, project);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_created ON cloud_observations(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_scope   ON cloud_observations(scope);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_topic   ON cloud_observations(topic_key, user_id, project, scope, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_deleted ON cloud_observations(deleted_at);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_dedupe  ON cloud_observations(normalized_hash, user_id, project, scope, type, title, created_at DESC);
```

All timestamp columns MUST use `TIMESTAMPTZ`. The `id` column MUST use `BIGSERIAL` (auto-incrementing 64-bit integer). The `user_id` column MUST be populated from the authenticated user context.

#### Scenario: Store an observation

- GIVEN an authenticated user and an active session "sess-abc"
- WHEN an observation is added with type "decision", title "Use JWT", content "We chose JWT for auth"
- THEN a row MUST be inserted into `cloud_observations`
- AND `user_id` MUST match the authenticated user
- AND `created_at` and `updated_at` MUST be set to NOW()
- AND `revision_count` MUST be 1
- AND `duplicate_count` MUST be 1

#### Scenario: Soft-delete an observation

- GIVEN an observation with id 42 and deleted_at NULL
- WHEN a soft delete is performed on observation 42
- THEN `deleted_at` MUST be set to the current timestamp
- AND the observation MUST NOT appear in default queries (which filter `deleted_at IS NULL`)

---

### PG-STORE-04: cloud_prompts Table

The system MUST store user prompts in a `cloud_prompts` table with the following schema:

```sql
CREATE TABLE IF NOT EXISTS cloud_prompts (
    id          BIGSERIAL   PRIMARY KEY,
    session_id  TEXT        NOT NULL REFERENCES cloud_sessions(id),
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    content     TEXT        NOT NULL,
    project     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_prompts_session ON cloud_prompts(session_id);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_user    ON cloud_prompts(user_id);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_project ON cloud_prompts(user_id, project);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_created ON cloud_prompts(created_at DESC);
```

#### Scenario: Store a prompt

- GIVEN an authenticated user and an active session
- WHEN a prompt is saved with content "How do I implement cloud sync?"
- THEN a row MUST be inserted into `cloud_prompts`
- AND `user_id` MUST match the authenticated user

---

### PG-STORE-05: cloud_chunks Table

The system MUST store sync chunks in a `cloud_chunks` table for tracking which data has been synced:

```sql
CREATE TABLE IF NOT EXISTS cloud_chunks (
    id          TEXT        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    created_by  TEXT        NOT NULL,
    sessions    INTEGER     NOT NULL DEFAULT 0,
    memories    INTEGER     NOT NULL DEFAULT 0,
    prompts     INTEGER     NOT NULL DEFAULT 0,
    data        JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_chunks_user ON cloud_chunks(user_id);

CREATE TABLE IF NOT EXISTS cloud_sync_chunks (
    chunk_id    TEXT        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

The `data` column MUST use JSONB to store the serialized chunk content (sessions, observations, prompts). The chunk `id` MUST be the SHA-256 hash prefix (8 hex chars) of the serialized content, matching the local sync format.

#### Scenario: Store a pushed chunk

- GIVEN an authenticated user pushing a sync chunk
- WHEN the chunk with id "a3f8c1d2" is received
- THEN a row MUST be inserted into `cloud_chunks` with the chunk data as JSONB
- AND `user_id` MUST match the authenticated user

#### Scenario: Reject duplicate chunk

- GIVEN a chunk with id "a3f8c1d2" already exists for user "u-123"
- WHEN the same chunk id is pushed again
- THEN the system MUST return a success response (idempotent)
- AND the existing chunk MUST NOT be modified

---

### PG-STORE-06: cloud_users Table

The system MUST store user accounts in a `cloud_users` table:

```sql
CREATE TABLE IF NOT EXISTS cloud_users (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT        NOT NULL UNIQUE,
    email           TEXT        NOT NULL UNIQUE,
    password_hash   TEXT        NOT NULL,
    api_key         TEXT        UNIQUE,
    api_key_hash    TEXT        UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_users_username ON cloud_users(username);
CREATE INDEX IF NOT EXISTS idx_cloud_users_email    ON cloud_users(email);
CREATE INDEX IF NOT EXISTS idx_cloud_users_api_key  ON cloud_users(api_key_hash);
```

The `id` column MUST use UUID with `gen_random_uuid()` default. The `password_hash` column MUST store bcrypt hashes (NEVER plain text). The `api_key_hash` column MUST store SHA-256 hashes of API keys for lookup. The plain `api_key` column MAY store a masked version for display (e.g., "eng_...last4") or MAY be omitted entirely.

#### Scenario: Create a user account

- GIVEN no user exists with username "alice"
- WHEN registration is requested with username "alice", email "alice@example.com", password "SecureP@ss1"
- THEN a row MUST be inserted with a generated UUID
- AND `password_hash` MUST be a bcrypt hash of "SecureP@ss1"
- AND the plain password MUST NOT be stored

#### Scenario: Reject duplicate username

- GIVEN a user exists with username "alice"
- WHEN registration is requested with username "alice"
- THEN the system MUST return an error indicating username is taken
- AND no new row MUST be inserted

---

### PG-STORE-07: Data Isolation

The system MUST enforce data isolation between users. All queries for sessions, observations, prompts, and chunks MUST filter by `user_id`. A user MUST NOT be able to read, modify, or delete another user's data.

#### Scenario: Query observations filtered by user

- GIVEN user "u-123" has 10 observations and user "u-456" has 5 observations
- WHEN user "u-123" queries their observations
- THEN exactly 10 observations MUST be returned
- AND none of user "u-456"'s observations MUST appear

#### Scenario: Prevent cross-user observation access

- GIVEN observation 42 belongs to user "u-456"
- WHEN user "u-123" attempts to read observation 42
- THEN the system MUST return a "not found" error
- AND the observation content MUST NOT be disclosed

---

## Domain 2: JWT Authentication (auth)

### Purpose

Provides user registration, login, token management, and API key authentication for the cloud server. Uses JWT (JSON Web Tokens) for session-based auth and SHA-256-hashed API keys for programmatic access.

---

### AUTH-01: User Registration

The system MUST allow new users to register with username, email, and password. The password MUST be hashed with bcrypt (cost >= 10) before storage. The system MUST validate that username and email are unique. The system SHOULD validate email format and password strength (minimum 8 characters).

#### Scenario: Successful registration

- GIVEN no user exists with username "bob" or email "bob@example.com"
- WHEN a registration request is made with username "bob", email "bob@example.com", password "MyStr0ngP@ss"
- THEN a new user MUST be created
- AND the response MUST include the user id and a JWT access token
- AND a refresh token MUST be issued
- AND the password MUST be stored as a bcrypt hash

#### Scenario: Registration with duplicate email

- GIVEN a user exists with email "bob@example.com"
- WHEN registration is attempted with a different username but email "bob@example.com"
- THEN the system MUST return HTTP 409 Conflict
- AND the error message MUST indicate email is already registered

#### Scenario: Registration with weak password

- GIVEN a registration request with password "123"
- WHEN the request is processed
- THEN the system MUST return HTTP 400 Bad Request
- AND the error MUST indicate password requirements

---

### AUTH-02: User Login

The system MUST authenticate users by username/email and password. On success, the system MUST return a JWT access token and a refresh token. The access token MUST expire (SHOULD be 15-60 minutes). The refresh token MUST have a longer expiry (SHOULD be 7-30 days).

#### Scenario: Successful login with username

- GIVEN a registered user with username "bob" and password "MyStr0ngP@ss"
- WHEN a login request is made with username "bob" and password "MyStr0ngP@ss"
- THEN the response MUST include an access_token and refresh_token
- AND the access_token MUST be a valid JWT containing the user_id claim
- AND the response MUST include token expiry information

#### Scenario: Login with incorrect password

- GIVEN a registered user with username "bob"
- WHEN a login request is made with username "bob" and password "wrongpassword"
- THEN the system MUST return HTTP 401 Unauthorized
- AND the error MUST NOT disclose whether the username exists

#### Scenario: Login with non-existent user

- GIVEN no user exists with username "nonexistent"
- WHEN a login request is made with username "nonexistent"
- THEN the system MUST return HTTP 401 Unauthorized
- AND the response time SHOULD be comparable to a failed password check (timing-safe)

---

### AUTH-03: Token Refresh

The system MUST allow refreshing an expired access token using a valid refresh token. The system MUST issue a new access token and MAY rotate the refresh token.

#### Scenario: Successful token refresh

- GIVEN a valid refresh token for user "bob"
- WHEN a refresh request is made with the refresh token
- THEN a new access_token MUST be issued
- AND the new token MUST contain the same user_id claim

#### Scenario: Refresh with expired token

- GIVEN a refresh token that has expired
- WHEN a refresh request is made
- THEN the system MUST return HTTP 401 Unauthorized
- AND the error MUST indicate the token has expired

#### Scenario: Refresh with invalid token

- GIVEN a malformed or tampered refresh token
- WHEN a refresh request is made
- THEN the system MUST return HTTP 401 Unauthorized

---

### AUTH-04: API Key Authentication

The system MUST support API key-based authentication as an alternative to JWT. API keys MUST be long-lived tokens prefixed with `eng_` for identification. The system MUST store only the SHA-256 hash of the API key. The plain API key MUST be shown to the user exactly once at creation time.

#### Scenario: Generate an API key

- GIVEN an authenticated user "bob"
- WHEN an API key generation is requested
- THEN the system MUST return a new API key prefixed with `eng_`
- AND the API key MUST be at least 32 characters long (including prefix)
- AND the SHA-256 hash of the key MUST be stored in `cloud_users.api_key_hash`
- AND the plain key MUST NOT be stored after initial display

#### Scenario: Authenticate with API key via header

- GIVEN a valid API key "eng_abc123..."
- WHEN a request is made with header `Authorization: Bearer eng_abc123...`
- THEN the system MUST hash the provided key and look up the user by `api_key_hash`
- AND the request MUST be authenticated as the corresponding user

#### Scenario: Authenticate with revoked API key

- GIVEN an API key that has been revoked (api_key_hash set to NULL)
- WHEN a request is made with the revoked key
- THEN the system MUST return HTTP 401 Unauthorized

---

### AUTH-05: JWT Middleware

The system MUST provide HTTP middleware that validates JWT tokens or API keys on protected routes. The middleware MUST extract the user_id from the token and inject it into the request context. Unauthenticated requests to protected routes MUST receive HTTP 401.

#### Scenario: Valid JWT in Authorization header

- GIVEN a valid JWT access token for user "u-123"
- WHEN a request is made with header `Authorization: Bearer <jwt_token>`
- THEN the middleware MUST extract user_id "u-123" from the token
- AND the user_id MUST be available in the request context
- AND the request MUST proceed to the handler

#### Scenario: Missing Authorization header

- GIVEN a request with no Authorization header
- WHEN the request hits a protected endpoint
- THEN the middleware MUST return HTTP 401 Unauthorized
- AND the response body MUST include an error message

#### Scenario: Expired JWT

- GIVEN a JWT access token that has expired
- WHEN a request is made with the expired token
- THEN the middleware MUST return HTTP 401 Unauthorized
- AND the error MUST indicate token expiration

#### Scenario: API key fallback in middleware

- GIVEN a valid API key "eng_abc123..."
- WHEN a request is made with header `Authorization: Bearer eng_abc123...`
- THEN the middleware MUST detect the `eng_` prefix
- AND authenticate via API key lookup instead of JWT verification
- AND the user_id MUST be injected into context

---

### AUTH-06: JWT Claims Structure

The JWT access token MUST contain the following claims:

| Claim | Type | Description |
|-------|------|-------------|
| `sub` | string (UUID) | User ID |
| `username` | string | Username |
| `exp` | number | Expiration time (Unix timestamp) |
| `iat` | number | Issued-at time (Unix timestamp) |
| `type` | string | Token type: "access" or "refresh" |

The system MUST use HMAC-SHA256 (HS256) for signing. The signing secret MUST be configurable via environment variable (`MNEMO_JWT_SECRET`). The secret MUST be at least 32 bytes.

#### Scenario: Decode a valid access token

- GIVEN a JWT issued for user "u-123" with username "bob"
- WHEN the token is decoded
- THEN `sub` MUST equal "u-123"
- AND `username` MUST equal "bob"
- AND `type` MUST equal "access"
- AND `exp` MUST be greater than `iat`

---

## Domain 3: Cloud Server (cloud/server)

### Purpose

An HTTP server that exposes the cloud Mnemo API, handling authentication, memory push/pull, search, and context retrieval. All routes (except auth endpoints) MUST be protected by the JWT middleware.

---

### CLOUD-SRV-01: Server Configuration

The cloud server MUST be configurable via environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MNEMO_CLOUD_PORT` | No | 8080 | Server listen port |
| `MNEMO_DATABASE_URL` | Yes | - | PostgreSQL connection string |
| `MNEMO_JWT_SECRET` | Yes | - | JWT signing secret (>= 32 bytes) |
| `MNEMO_BCRYPT_COST` | No | 12 | bcrypt hashing cost |

The server MUST fail to start if required environment variables are missing.

#### Scenario: Start server with valid config

- GIVEN `MNEMO_DATABASE_URL` and `MNEMO_JWT_SECRET` are set
- WHEN the cloud server starts
- THEN it MUST listen on the configured port
- AND the health endpoint MUST respond with 200

#### Scenario: Start server with missing DATABASE_URL

- GIVEN `MNEMO_DATABASE_URL` is not set
- WHEN the cloud server attempts to start
- THEN it MUST exit with a non-zero code
- AND the error message MUST indicate the missing variable

---

### CLOUD-SRV-02: Health Endpoint

The system MUST expose `GET /health` without authentication.

**Response (200 OK):**
```json
{
    "status": "ok",
    "service": "mnemo-cloud",
    "version": "0.1.0"
}
```

#### Scenario: Health check

- GIVEN the cloud server is running
- WHEN `GET /health` is called without authentication
- THEN the response MUST be HTTP 200
- AND the body MUST contain `"status": "ok"` and `"service": "mnemo-cloud"`

---

### CLOUD-SRV-03: Auth Endpoints

The system MUST expose the following unauthenticated endpoints:

**POST /auth/register**

Request:
```json
{
    "username": "string",
    "email": "string",
    "password": "string"
}
```

Response (201 Created):
```json
{
    "user_id": "uuid",
    "username": "string",
    "access_token": "string",
    "refresh_token": "string",
    "expires_in": 3600
}
```

**POST /auth/login**

Request:
```json
{
    "username": "string",
    "password": "string"
}
```

Response (200 OK):
```json
{
    "user_id": "uuid",
    "username": "string",
    "access_token": "string",
    "refresh_token": "string",
    "expires_in": 3600
}
```

**POST /auth/refresh**

Request:
```json
{
    "refresh_token": "string"
}
```

Response (200 OK):
```json
{
    "access_token": "string",
    "expires_in": 3600
}
```

#### Scenario: Full registration and login flow

- GIVEN the cloud server is running
- WHEN a user registers via `POST /auth/register`
- THEN the response MUST include access_token and refresh_token
- AND when the user logs in via `POST /auth/login` with the same credentials
- THEN a new access_token MUST be issued

#### Scenario: Register with invalid JSON

- GIVEN a request to `POST /auth/register` with malformed JSON
- WHEN the request is processed
- THEN the response MUST be HTTP 400
- AND the body MUST contain an error message

---

### CLOUD-SRV-04: Push Endpoint

The system MUST expose `POST /sync/push` (authenticated) to receive sync chunks from clients.

**Request:**
```json
{
    "chunk_id": "string (8-char hex)",
    "created_by": "string",
    "data": {
        "sessions": [...],
        "observations": [...],
        "prompts": [...]
    }
}
```

**Response (200 OK):**
```json
{
    "status": "accepted",
    "chunk_id": "string",
    "sessions_stored": 3,
    "observations_stored": 15,
    "prompts_stored": 7
}
```

The system MUST validate the chunk_id format (8 hex characters). The system MUST store the chunk data in `cloud_chunks` and individually insert sessions, observations, and prompts into their respective tables with the authenticated user's `user_id`. The system MUST handle duplicate chunk pushes idempotently.

#### Scenario: Push a new chunk

- GIVEN an authenticated user with a valid token
- WHEN `POST /sync/push` is called with a valid chunk containing 2 sessions and 5 observations
- THEN the response MUST be HTTP 200 with status "accepted"
- AND `sessions_stored` MUST equal 2
- AND `observations_stored` MUST equal 5

#### Scenario: Push a duplicate chunk

- GIVEN chunk "a3f8c1d2" was already pushed by this user
- WHEN `POST /sync/push` is called again with chunk_id "a3f8c1d2"
- THEN the response MUST be HTTP 200 with status "accepted"
- AND the data MUST NOT be duplicated

#### Scenario: Push without authentication

- GIVEN a request with no Authorization header
- WHEN `POST /sync/push` is called
- THEN the response MUST be HTTP 401 Unauthorized

---

### CLOUD-SRV-05: Pull Endpoint

The system MUST expose `GET /sync/pull` (authenticated) to send chunk manifests and chunk data to clients.

**GET /sync/pull** returns the manifest of all chunks for the authenticated user:

**Response (200 OK):**
```json
{
    "version": 1,
    "chunks": [
        {
            "id": "a3f8c1d2",
            "created_by": "alice",
            "created_at": "2026-03-07T10:00:00Z",
            "sessions": 2,
            "memories": 5,
            "prompts": 3
        }
    ]
}
```

**GET /sync/pull/{chunk_id}** returns the full data for a specific chunk:

**Response (200 OK):**
```json
{
    "sessions": [...],
    "observations": [...],
    "prompts": [...]
}
```

The manifest MUST only include chunks belonging to the authenticated user. The system MUST return HTTP 404 if a chunk_id does not exist or belongs to another user.

#### Scenario: Pull manifest with chunks

- GIVEN user "u-123" has pushed 3 chunks
- WHEN `GET /sync/pull` is called by user "u-123"
- THEN the response MUST contain a manifest with exactly 3 chunk entries

#### Scenario: Pull a specific chunk

- GIVEN chunk "a3f8c1d2" belongs to user "u-123"
- WHEN `GET /sync/pull/a3f8c1d2` is called by user "u-123"
- THEN the response MUST contain the full chunk data (sessions, observations, prompts)

#### Scenario: Pull another user's chunk

- GIVEN chunk "a3f8c1d2" belongs to user "u-456"
- WHEN `GET /sync/pull/a3f8c1d2` is called by user "u-123"
- THEN the response MUST be HTTP 404

---

### CLOUD-SRV-06: Search Endpoint

The system MUST expose `GET /sync/search` (authenticated) for full-text search over the user's cloud observations.

**Query Parameters:**

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `q` | Yes | - | Search query string |
| `type` | No | - | Filter by observation type |
| `project` | No | - | Filter by project |
| `scope` | No | - | Filter by scope |
| `limit` | No | 10 | Maximum results |

**Response (200 OK):**
```json
{
    "results": [
        {
            "id": 42,
            "session_id": "sess-abc",
            "type": "decision",
            "title": "Use JWT",
            "content": "We chose JWT for auth...",
            "project": "mnemo",
            "scope": "project",
            "rank": 0.95,
            "created_at": "2026-03-07T10:00:00Z",
            "updated_at": "2026-03-07T10:00:00Z"
        }
    ]
}
```

Results MUST be filtered by the authenticated user's `user_id`. Results MUST be ordered by relevance rank (descending).

#### Scenario: Search with results

- GIVEN user "u-123" has observations containing the word "authentication"
- WHEN `GET /sync/search?q=authentication` is called by user "u-123"
- THEN the response MUST contain matching observations
- AND results MUST be ordered by rank descending

#### Scenario: Search with no results

- GIVEN user "u-123" has no observations matching "xyznonexistent"
- WHEN `GET /sync/search?q=xyznonexistent` is called
- THEN the response MUST be HTTP 200
- AND `results` MUST be an empty array

#### Scenario: Search requires q parameter

- GIVEN an authenticated request
- WHEN `GET /sync/search` is called without the `q` parameter
- THEN the response MUST be HTTP 400
- AND the error MUST indicate that `q` is required

---

### CLOUD-SRV-07: Context Endpoint

The system MUST expose `GET /sync/context` (authenticated) that returns a formatted context string from the user's cloud observations, matching the local `/context` endpoint behavior.

**Query Parameters:**

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `project` | No | - | Filter by project |
| `scope` | No | - | Filter by scope |

**Response (200 OK):**
```json
{
    "context": "# Session: mnemo (2026-03-07)\n\n## Decisions\n- Use JWT for auth\n..."
}
```

#### Scenario: Get context for a project

- GIVEN user "u-123" has observations for project "mnemo"
- WHEN `GET /sync/context?project=mnemo` is called
- THEN the response MUST contain a formatted context string
- AND the context MUST only include observations belonging to user "u-123"

---

### CLOUD-SRV-08: API Key Management Endpoint

The system MUST expose `POST /auth/api-key` (authenticated) to generate or rotate API keys.

**Response (201 Created):**
```json
{
    "api_key": "eng_a1b2c3d4e5f6...",
    "message": "Store this key securely. It will not be shown again."
}
```

The system MUST expose `DELETE /auth/api-key` (authenticated) to revoke the current API key.

**Response (200 OK):**
```json
{
    "status": "revoked"
}
```

#### Scenario: Generate API key

- GIVEN an authenticated user with no existing API key
- WHEN `POST /auth/api-key` is called
- THEN the response MUST include a new API key prefixed with `eng_`
- AND the response status MUST be 201

#### Scenario: Rotate API key

- GIVEN an authenticated user with an existing API key
- WHEN `POST /auth/api-key` is called
- THEN the old API key MUST be invalidated
- AND a new API key MUST be returned

#### Scenario: Revoke API key

- GIVEN an authenticated user with an existing API key
- WHEN `DELETE /auth/api-key` is called
- THEN the API key hash MUST be removed from the database
- AND subsequent requests with the old key MUST fail with HTTP 401

---

## Domain 4: Remote Sync Transport (sync)

### Purpose

An HTTP client that communicates with the cloud server to push local chunks and pull remote chunks. This extends the existing `internal/sync` package with remote transport capabilities.

---

### SYNC-01: Remote Push

The system MUST provide a function to push a local chunk to the cloud server via `POST /sync/push`. The function MUST serialize the chunk in the same format as the local sync (sessions, observations, prompts). The function MUST include the authentication token in the `Authorization` header.

#### Scenario: Push a local chunk to cloud

- GIVEN a local chunk "a3f8c1d2" with 2 sessions and 5 observations
- AND a valid auth token for the cloud server
- WHEN `RemotePush` is called with the chunk data
- THEN the system MUST send a POST request to `{cloud_url}/sync/push`
- AND the request body MUST contain the chunk data as JSON
- AND the `Authorization: Bearer <token>` header MUST be set

#### Scenario: Push fails due to network error

- GIVEN the cloud server is unreachable
- WHEN `RemotePush` is called
- THEN the function MUST return an error indicating network failure
- AND the local chunk MUST NOT be marked as synced

---

### SYNC-02: Remote Pull Manifest

The system MUST provide a function to pull the chunk manifest from the cloud server via `GET /sync/pull`. The function MUST return the list of chunk entries available on the server.

#### Scenario: Pull manifest from cloud

- GIVEN the cloud server has 5 chunks for the authenticated user
- WHEN `RemotePullManifest` is called
- THEN the function MUST return a manifest with 5 chunk entries
- AND each entry MUST include id, created_by, created_at, sessions, memories, and prompts counts

---

### SYNC-03: Remote Pull Chunk

The system MUST provide a function to download a specific chunk from the cloud server via `GET /sync/pull/{chunk_id}`. The function MUST deserialize the response into the standard `ChunkData` format.

#### Scenario: Download a specific chunk

- GIVEN chunk "a3f8c1d2" exists on the cloud server
- WHEN `RemotePullChunk("a3f8c1d2")` is called
- THEN the function MUST return the deserialized ChunkData
- AND the data MUST contain valid sessions, observations, and prompts arrays

#### Scenario: Download non-existent chunk

- GIVEN chunk "00000000" does not exist on the cloud server
- WHEN `RemotePullChunk("00000000")` is called
- THEN the function MUST return an error indicating the chunk was not found

---

### SYNC-04: Remote Sync Orchestration

The system MUST provide a high-level `RemoteSync` function that:
1. Exports local data to a new chunk (using existing `Export`)
2. Pushes the chunk to the cloud server (if non-empty)
3. Pulls the remote manifest
4. Downloads and imports any chunks not present locally

The system MUST track which chunks have been synced to/from the cloud to avoid re-processing.

#### Scenario: Full sync with new local and remote data

- GIVEN the local store has new observations not yet exported
- AND the cloud server has 2 chunks not present locally
- WHEN `RemoteSync` is called
- THEN a new local chunk MUST be created and pushed to the cloud
- AND 2 remote chunks MUST be downloaded and imported
- AND the sync result MUST report counts for both push and pull

#### Scenario: Sync with nothing new

- GIVEN the local store has no new data since last sync
- AND the cloud server has no new chunks
- WHEN `RemoteSync` is called
- THEN the function MUST return a result indicating nothing was synced
- AND no HTTP requests MUST be made for chunk data (only the manifest check)

---

### SYNC-05: Remote Client Configuration

The system MUST accept cloud connection configuration:

| Field | Required | Description |
|-------|----------|-------------|
| `server_url` | Yes | Cloud server base URL (e.g., `https://mnemo.example.com`) |
| `token` | Yes | JWT access token or API key (`eng_` prefixed) |

The system MUST validate the URL format. The system MUST support both HTTPS and HTTP (for local development). The system SHOULD warn when using HTTP in non-localhost contexts.

#### Scenario: Configure remote client

- GIVEN server_url "https://mnemo.example.com" and a valid token
- WHEN a RemoteClient is created
- THEN the client MUST store the URL and token
- AND all subsequent requests MUST use the configured URL as base

#### Scenario: Reject invalid server URL

- GIVEN server_url "not-a-url"
- WHEN a RemoteClient is created
- THEN the constructor MUST return an error

---

## Domain 5: CLI Integration (cmd/mnemo)

### Purpose

Extends the existing CLI with flags and subcommands for cloud sync operations. The local mode MUST remain the default and MUST NOT be affected by cloud features.

---

### CLI-01: Remote Flags

The system MUST add the following global flags to the CLI:

| Flag | Short | Env Var | Description |
|------|-------|---------|-------------|
| `--remote` | `-r` | `MNEMO_REMOTE_URL` | Cloud server URL |
| `--token` | `-t` | `MNEMO_TOKEN` | Authentication token |

When `--remote` is provided, applicable commands (search, context, sync) MUST operate against the cloud server instead of the local SQLite store. The token MAY also be read from a config file at `~/.mnemo/cloud.json`.

#### Scenario: Search against cloud

- GIVEN `--remote https://mnemo.example.com --token eng_abc123`
- WHEN `mnemo search "authentication"` is run
- THEN the search MUST be performed against the cloud server's `/sync/search` endpoint
- AND the results MUST be displayed in the same format as local search

#### Scenario: Default to local mode

- GIVEN no `--remote` flag is provided
- WHEN `mnemo search "authentication"` is run
- THEN the search MUST be performed against the local SQLite store
- AND no network requests MUST be made

---

### CLI-02: Cloud Subcommand

The system MUST add an `mnemo cloud` subcommand group:

| Subcommand | Description |
|------------|-------------|
| `mnemo cloud register` | Register a new account |
| `mnemo cloud login` | Login and save token |
| `mnemo cloud sync` | Push and pull chunks |
| `mnemo cloud status` | Show sync status |
| `mnemo cloud api-key` | Generate/show API key |

#### Scenario: Cloud register

- GIVEN no saved credentials
- WHEN `mnemo cloud register --server https://mnemo.example.com` is run
- THEN the CLI MUST prompt for username, email, and password
- AND on success, the token MUST be saved to `~/.mnemo/cloud.json`

#### Scenario: Cloud sync

- GIVEN valid credentials saved in `~/.mnemo/cloud.json`
- WHEN `mnemo cloud sync` is run
- THEN the CLI MUST push local chunks and pull remote chunks
- AND a summary MUST be printed showing what was synced

---

### CLI-03: Cloud Configuration File

The system MUST support saving cloud credentials to `~/.mnemo/cloud.json`:

```json
{
    "server_url": "https://mnemo.example.com",
    "token": "eng_abc123...",
    "user_id": "uuid",
    "username": "alice"
}
```

The file MUST be created with permissions `0600` (owner read/write only). The system MUST prefer CLI flags over config file values. The system MUST prefer environment variables over config file values.

#### Scenario: Load token from config file

- GIVEN `~/.mnemo/cloud.json` contains a valid token
- AND no `--token` flag is provided
- WHEN `mnemo cloud sync` is run
- THEN the token from the config file MUST be used

#### Scenario: CLI flag overrides config file

- GIVEN `~/.mnemo/cloud.json` contains token "eng_old"
- AND `--token eng_new` is provided
- WHEN a cloud command is run
- THEN the token "eng_new" MUST be used

---

### CLI-04: Cloud Server Command

The system MUST add `mnemo cloud serve` to start the cloud server:

```
mnemo cloud serve [--port 8080] [--database-url postgres://...]
```

This MUST start the cloud HTTP server with PostgreSQL backend. Configuration MUST fall back to environment variables if flags are not provided.

#### Scenario: Start cloud server via CLI

- GIVEN `MNEMO_DATABASE_URL` is set
- WHEN `mnemo cloud serve --port 9090` is run
- THEN the cloud server MUST start on port 9090
- AND the health endpoint MUST respond at `http://localhost:9090/health`

#### Scenario: Start without database URL

- GIVEN `MNEMO_DATABASE_URL` is not set and `--database-url` is not provided
- WHEN `mnemo cloud serve` is run
- THEN the CLI MUST exit with an error message indicating the database URL is required

---

## Domain 6: Cloud Full-Text Search (FTS)

### Purpose

PostgreSQL-native full-text search using tsvector/tsquery for cloud observations and prompts, replacing SQLite FTS5 in the cloud context.

---

### FTS-01: Observation Search Vector

The system MUST maintain a tsvector column on `cloud_observations` for full-text search:

```sql
ALTER TABLE cloud_observations ADD COLUMN IF NOT EXISTS search_vector tsvector;

CREATE INDEX IF NOT EXISTS idx_cloud_obs_fts ON cloud_observations USING GIN(search_vector);

CREATE OR REPLACE FUNCTION cloud_obs_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', COALESCE(NEW.title, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.content, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.tool_name, '')), 'C') ||
        setweight(to_tsvector('english', COALESCE(NEW.type, '')), 'C') ||
        setweight(to_tsvector('english', COALESCE(NEW.project, '')), 'C');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cloud_obs_fts_trigger
    BEFORE INSERT OR UPDATE ON cloud_observations
    FOR EACH ROW EXECUTE FUNCTION cloud_obs_search_vector_update();
```

Title MUST have weight 'A' (highest relevance). Content MUST have weight 'B'. Tool name, type, and project MUST have weight 'C'.

#### Scenario: Search matches title with higher rank

- GIVEN observation A has "authentication" in its title
- AND observation B has "authentication" only in its content
- WHEN a search for "authentication" is performed
- THEN observation A MUST rank higher than observation B

#### Scenario: Search with multiple terms

- GIVEN observations containing "JWT", "authentication", and "JWT authentication"
- WHEN a search for "JWT authentication" is performed
- THEN observations containing both terms MUST rank highest

---

### FTS-02: Prompt Search Vector

The system MUST maintain a tsvector column on `cloud_prompts` for full-text search:

```sql
ALTER TABLE cloud_prompts ADD COLUMN IF NOT EXISTS search_vector tsvector;

CREATE INDEX IF NOT EXISTS idx_cloud_prompts_fts ON cloud_prompts USING GIN(search_vector);

CREATE OR REPLACE FUNCTION cloud_prompts_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', COALESCE(NEW.content, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.project, '')), 'B');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cloud_prompts_fts_trigger
    BEFORE INSERT OR UPDATE ON cloud_prompts
    FOR EACH ROW EXECUTE FUNCTION cloud_prompts_search_vector_update();
```

#### Scenario: Search prompts

- GIVEN user "u-123" has prompts containing "refactor authentication"
- WHEN a prompt search for "refactor" is performed
- THEN matching prompts MUST be returned
- AND results MUST be filtered by user_id

---

### FTS-03: Search Query Parsing

The system MUST convert user search queries to PostgreSQL tsquery format. The system MUST support:
- Simple word queries: `"authentication"` -> `to_tsquery('english', 'authentication')`
- Multi-word queries: `"JWT auth"` -> `to_tsquery('english', 'JWT & auth')`
- Prefix matching: `"auth*"` -> `to_tsquery('english', 'auth:*')`

The system SHOULD handle special characters gracefully by stripping or escaping them.

#### Scenario: Simple word search

- GIVEN the query string "authentication"
- WHEN the query is parsed
- THEN it MUST produce a valid tsquery matching "authentication" and its stems

#### Scenario: Query with special characters

- GIVEN the query string "what's the auth?"
- WHEN the query is parsed
- THEN special characters MUST be handled without SQL errors
- AND the search MUST still return relevant results

---

## Non-Functional Requirements

### NFR-01: Search Performance

Cloud search queries MUST respond within 500ms for datasets up to 100,000 observations per user. The PostgreSQL tsvector/GIN index MUST be used for all full-text queries. The system SHOULD support connection pooling for concurrent users.

### NFR-02: Security

- Passwords MUST be hashed with bcrypt (cost >= 10)
- JWT secrets MUST be at least 32 bytes
- API keys MUST be at least 32 characters
- API key storage MUST use SHA-256 hashing (never plain text)
- The cloud server SHOULD be deployed behind TLS (HTTPS)
- The system MUST NOT log tokens, passwords, or API keys
- SQL queries MUST use parameterized statements (no string concatenation)

### NFR-03: Local Mode Compatibility

The cloud sync feature MUST NOT break any existing local-mode functionality. When `--remote` is not specified, the system MUST behave identically to the current version. No existing tests MUST be broken by the addition of cloud features. The local SQLite store MUST remain the default.

### NFR-04: Database Connection Resilience

The cloud server MUST handle PostgreSQL connection failures gracefully. The system SHOULD implement connection retry with exponential backoff (max 3 retries). The system MUST return HTTP 503 Service Unavailable when the database is unreachable.

### NFR-05: Chunk Format Compatibility

Cloud chunks MUST use the same JSON format as local chunks (defined in `internal/sync`). The `ChunkData` struct (sessions, observations, prompts) MUST be the canonical format for both local and cloud sync. This ensures a client can sync locally via git AND remotely via cloud without format translation.

---

## Error Scenarios

### ERR-01: Network Failure During Push

- GIVEN a client is pushing a chunk to the cloud server
- WHEN the network connection is lost mid-transfer
- THEN the client MUST detect the failure and return an error
- AND the local chunk MUST NOT be marked as synced to cloud
- AND the chunk file MUST remain on disk for retry

### ERR-02: Network Failure During Pull

- GIVEN a client is downloading a chunk from the cloud server
- WHEN the network connection is lost mid-transfer
- THEN the client MUST detect the failure and return an error
- AND no partial data MUST be imported into the local store
- AND the chunk MUST NOT be marked as imported

### ERR-03: Token Expiry During Sync

- GIVEN a client starts a sync operation with a valid token
- WHEN the token expires mid-operation (between push and pull)
- THEN the client MUST detect the 401 response
- AND the client SHOULD attempt to refresh the token if a refresh token is available
- AND if refresh fails, the error MUST indicate authentication failure

### ERR-04: Partial Sync Recovery

- GIVEN a sync operation pushed 1 chunk but failed during pull
- WHEN sync is retried
- THEN the already-pushed chunk MUST NOT be pushed again (idempotent)
- AND the pull MUST resume, downloading only missing chunks

### ERR-05: PostgreSQL Connection Failure

- GIVEN the cloud server is running
- WHEN the PostgreSQL connection is lost
- THEN all data endpoints MUST return HTTP 503 Service Unavailable
- AND the health endpoint SHOULD return a degraded status
- AND the server MUST attempt to reconnect on subsequent requests

### ERR-06: Rate Limiting

The cloud server SHOULD implement rate limiting on authentication endpoints to prevent brute-force attacks:
- `POST /auth/login`: SHOULD allow no more than 10 attempts per minute per IP
- `POST /auth/register`: SHOULD allow no more than 5 attempts per minute per IP

- GIVEN a client has made 10 login attempts in the last minute
- WHEN an 11th attempt is made
- THEN the response SHOULD be HTTP 429 Too Many Requests
- AND the response SHOULD include a `Retry-After` header

### ERR-07: Oversized Chunk Rejection

The system MUST reject chunks larger than 50MB.

- GIVEN a client pushes a chunk that is 60MB
- WHEN the server receives the request
- THEN the response MUST be HTTP 413 Payload Too Large
- AND the chunk MUST NOT be stored

### ERR-08: Invalid Chunk Format

- GIVEN a client pushes a chunk with invalid JSON in the data field
- WHEN the server attempts to parse the chunk
- THEN the response MUST be HTTP 400 Bad Request
- AND the error message MUST indicate the parse failure
- AND no partial data MUST be stored

---

## Appendix A: Complete PostgreSQL Schema

```sql
-- ============================================================
-- Mnemo Cloud Schema
-- ============================================================

-- Users
CREATE TABLE IF NOT EXISTS cloud_users (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT        NOT NULL UNIQUE,
    email           TEXT        NOT NULL UNIQUE,
    password_hash   TEXT        NOT NULL,
    api_key         TEXT        UNIQUE,
    api_key_hash    TEXT        UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_users_username ON cloud_users(username);
CREATE INDEX IF NOT EXISTS idx_cloud_users_email    ON cloud_users(email);
CREATE INDEX IF NOT EXISTS idx_cloud_users_api_key  ON cloud_users(api_key_hash);

-- Sessions
CREATE TABLE IF NOT EXISTS cloud_sessions (
    id          TEXT        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    project     TEXT        NOT NULL,
    directory   TEXT        NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    summary     TEXT
);

CREATE INDEX IF NOT EXISTS idx_cloud_sessions_user    ON cloud_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_cloud_sessions_project ON cloud_sessions(user_id, project);

-- Observations
CREATE TABLE IF NOT EXISTS cloud_observations (
    id              BIGSERIAL   PRIMARY KEY,
    session_id      TEXT        NOT NULL REFERENCES cloud_sessions(id),
    user_id         UUID        NOT NULL REFERENCES cloud_users(id),
    type            TEXT        NOT NULL,
    title           TEXT        NOT NULL,
    content         TEXT        NOT NULL,
    tool_name       TEXT,
    project         TEXT,
    scope           TEXT        NOT NULL DEFAULT 'project',
    topic_key       TEXT,
    normalized_hash TEXT,
    revision_count  INTEGER     NOT NULL DEFAULT 1,
    duplicate_count INTEGER     NOT NULL DEFAULT 1,
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    search_vector   tsvector
);

CREATE INDEX IF NOT EXISTS idx_cloud_obs_session ON cloud_observations(session_id);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user    ON cloud_observations(user_id);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_type    ON cloud_observations(type);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_project ON cloud_observations(user_id, project);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_created ON cloud_observations(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_scope   ON cloud_observations(scope);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_topic   ON cloud_observations(topic_key, user_id, project, scope, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_deleted ON cloud_observations(deleted_at);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_dedupe  ON cloud_observations(normalized_hash, user_id, project, scope, type, title, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_fts     ON cloud_observations USING GIN(search_vector);

-- Observations FTS trigger
CREATE OR REPLACE FUNCTION cloud_obs_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', COALESCE(NEW.title, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.content, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.tool_name, '')), 'C') ||
        setweight(to_tsvector('english', COALESCE(NEW.type, '')), 'C') ||
        setweight(to_tsvector('english', COALESCE(NEW.project, '')), 'C');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cloud_obs_fts_trigger
    BEFORE INSERT OR UPDATE ON cloud_observations
    FOR EACH ROW EXECUTE FUNCTION cloud_obs_search_vector_update();

-- Prompts
CREATE TABLE IF NOT EXISTS cloud_prompts (
    id          BIGSERIAL   PRIMARY KEY,
    session_id  TEXT        NOT NULL REFERENCES cloud_sessions(id),
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    content     TEXT        NOT NULL,
    project     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    search_vector tsvector
);

CREATE INDEX IF NOT EXISTS idx_cloud_prompts_session ON cloud_prompts(session_id);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_user    ON cloud_prompts(user_id);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_project ON cloud_prompts(user_id, project);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_created ON cloud_prompts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_fts     ON cloud_prompts USING GIN(search_vector);

-- Prompts FTS trigger
CREATE OR REPLACE FUNCTION cloud_prompts_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', COALESCE(NEW.content, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.project, '')), 'B');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cloud_prompts_fts_trigger
    BEFORE INSERT OR UPDATE ON cloud_prompts
    FOR EACH ROW EXECUTE FUNCTION cloud_prompts_search_vector_update();

-- Chunks (stores pushed sync chunks)
CREATE TABLE IF NOT EXISTS cloud_chunks (
    id          TEXT        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    created_by  TEXT        NOT NULL,
    sessions    INTEGER     NOT NULL DEFAULT 0,
    memories    INTEGER     NOT NULL DEFAULT 0,
    prompts     INTEGER     NOT NULL DEFAULT 0,
    data        JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_chunks_user ON cloud_chunks(user_id);

-- Sync tracking (which chunks a user has pulled/imported)
CREATE TABLE IF NOT EXISTS cloud_sync_chunks (
    chunk_id    TEXT        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES cloud_users(id),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_sync_user ON cloud_sync_chunks(user_id);
```

---

## Appendix B: API Route Summary

### Unauthenticated Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/auth/register` | User registration |
| POST | `/auth/login` | User login |
| POST | `/auth/refresh` | Token refresh |

### Authenticated Routes (JWT or API Key required)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/sync/push` | Push a sync chunk |
| GET | `/sync/pull` | Get chunk manifest |
| GET | `/sync/pull/{chunk_id}` | Download a specific chunk |
| GET | `/sync/search` | Full-text search observations |
| GET | `/sync/context` | Get formatted context |
| POST | `/auth/api-key` | Generate/rotate API key |
| DELETE | `/auth/api-key` | Revoke API key |

### Standard Error Response Format

All error responses MUST use this format:
```json
{
    "error": "human-readable error message"
}
```

This matches the existing local server error format (see `jsonError` in `internal/server/server.go`).

---

## Appendix C: Local vs Cloud Model Mapping

The cloud models mirror the local SQLite models with these differences:

| Local (SQLite) | Cloud (PostgreSQL) | Difference |
|----------------|-------------------|------------|
| `sessions` | `cloud_sessions` | Added `user_id UUID` column |
| `observations` | `cloud_observations` | Added `user_id UUID` column, `BIGSERIAL` id, `tsvector` column |
| `user_prompts` | `cloud_prompts` | Added `user_id UUID` column, `BIGSERIAL` id, `tsvector` column |
| `sync_chunks` | `cloud_sync_chunks` | Added `user_id UUID` column |
| N/A | `cloud_users` | New table |
| N/A | `cloud_chunks` | New table (stores chunk JSONB data) |
| `observations_fts` (FTS5) | `search_vector` column | PostgreSQL native tsvector replaces SQLite FTS5 |
| `prompts_fts` (FTS5) | `search_vector` column | PostgreSQL native tsvector replaces SQLite FTS5 |

The `ChunkData` struct from `internal/sync` (sessions, observations, prompts) is the canonical wire format for both local and cloud sync.
