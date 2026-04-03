package cloudstore

// schemaDDL contains all CREATE TABLE IF NOT EXISTS statements for the
// Mnemo Cloud Postgres schema. It is executed inside a single transaction
// during CloudStore initialization to guarantee atomicity and idempotency.
const schemaDDL = `
-- ── Users ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cloud_users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL UNIQUE,
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    api_key_hash    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cloud_users_username    ON cloud_users(username);
CREATE INDEX IF NOT EXISTS idx_cloud_users_email       ON cloud_users(email);
CREATE INDEX IF NOT EXISTS idx_cloud_users_api_key     ON cloud_users(api_key_hash) WHERE api_key_hash IS NOT NULL;

-- ── Sessions ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cloud_sessions (
    id          TEXT NOT NULL,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    project     TEXT NOT NULL,
    directory   TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    summary     TEXT,
    PRIMARY KEY (user_id, id)
);
CREATE INDEX IF NOT EXISTS idx_cloud_sessions_project ON cloud_sessions(user_id, project);

-- ── Observations ────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cloud_observations (
    id              BIGSERIAL PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
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
    tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(content, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(type, '') || ' ' || coalesce(project, '')), 'C')
    ) STORED
);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user_session  ON cloud_observations(user_id, session_id);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user_project  ON cloud_observations(user_id, project);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user_type     ON cloud_observations(user_id, type);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user_created  ON cloud_observations(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user_topic    ON cloud_observations(user_id, topic_key, project, scope, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user_dedupe   ON cloud_observations(user_id, normalized_hash, project, scope, type, title);
CREATE INDEX IF NOT EXISTS idx_cloud_obs_user_deleted  ON cloud_observations(user_id, deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cloud_obs_tsv           ON cloud_observations USING GIN(tsv);

-- ── Prompts ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cloud_prompts (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL,
    content     TEXT NOT NULL,
    project     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Full-text search vector for prompt content
    tsv tsvector GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(content, ''))
    ) STORED
);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_user_session ON cloud_prompts(user_id, session_id);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_user_project ON cloud_prompts(user_id, project);
CREATE INDEX IF NOT EXISTS idx_cloud_prompts_tsv          ON cloud_prompts USING GIN(tsv);

-- ── Project Controls (cloud-managed sync policy) ─────────────────────────
CREATE TABLE IF NOT EXISTS cloud_project_controls (
    project      TEXT PRIMARY KEY,
    sync_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    paused_reason TEXT,
    updated_by   UUID REFERENCES cloud_users(id) ON DELETE SET NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cloud_project_controls_enabled ON cloud_project_controls(sync_enabled);

-- ── Chunks (raw chunk storage for sync) ─────────────────────────────────
CREATE TABLE IF NOT EXISTS cloud_chunks (
    chunk_id    TEXT NOT NULL,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    data        BYTEA,
    created_by  TEXT NOT NULL DEFAULT '',
    sessions    INTEGER NOT NULL DEFAULT 0,
    memories    INTEGER NOT NULL DEFAULT 0,
    prompts     INTEGER NOT NULL DEFAULT 0,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, chunk_id)
);

-- ── Sync Chunks (tracking which chunks have been synced) ────────────────
CREATE TABLE IF NOT EXISTS cloud_sync_chunks (
    chunk_id    TEXT NOT NULL,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    synced_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, chunk_id)
);

-- ── Mutation Ledger (append-only per-user mutation journal for sync) ────
CREATE TABLE IF NOT EXISTS cloud_mutations (
    seq         BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    entity      TEXT NOT NULL,
    entity_key  TEXT NOT NULL,
    op          TEXT NOT NULL,
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cloud_mutations_user_seq ON cloud_mutations(user_id, seq);

-- ── Agent Tool Call Traces (jarvis-dashboard) ──────────────────────────
CREATE TABLE IF NOT EXISTS agent_tool_calls (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL,
    project     TEXT,
    agent       TEXT NOT NULL,
    tool_name   TEXT NOT NULL,
    input_json  JSONB,
    output_text TEXT,
    duration_ms INTEGER,
    tokens_in   INTEGER,
    tokens_out  INTEGER,
    model       TEXT,
    cost_usd    DECIMAL(12,8),
    is_engram   BOOLEAN NOT NULL DEFAULT FALSE,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_user_session ON agent_tool_calls(user_id, session_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_user_project ON agent_tool_calls(user_id, project);
CREATE INDEX IF NOT EXISTS idx_tool_calls_user_tool    ON agent_tool_calls(user_id, tool_name);
CREATE INDEX IF NOT EXISTS idx_tool_calls_user_time    ON agent_tool_calls(user_id, occurred_at DESC);

-- ── Tasks (jarvis-mvp) ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS tasks (
    id              BIGSERIAL PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    parent_id       BIGINT REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'open',
    priority        TEXT NOT NULL DEFAULT 'medium',
    assignee_type   TEXT NOT NULL DEFAULT 'user',
    assignee        TEXT,
    source          TEXT NOT NULL DEFAULT 'user',
    source_session_id TEXT,
    project         TEXT,
    tags            TEXT[],
    due_at          TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_tasks_user_status  ON tasks(user_id, status);
CREATE INDEX IF NOT EXISTS idx_tasks_user_project ON tasks(user_id, project);
CREATE INDEX IF NOT EXISTS idx_tasks_parent       ON tasks(parent_id);

-- ── Task Events ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS task_events (
    id          BIGSERIAL PRIMARY KEY,
    task_id     BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    payload     JSONB,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events(task_id);

-- ── Conversations (jarvis chat) ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS conversations (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    title       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id, updated_at DESC);

-- ── Messages ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS messages (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    model           TEXT,
    tokens_in       INTEGER,
    tokens_out      INTEGER,
    cost_usd        DECIMAL(12,8),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, created_at);

-- ── NOTIFY triggers for real-time ──────────────────────────────────────────
CREATE OR REPLACE FUNCTION notify_task_change() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('jarvis_tasks', json_build_object('op', TG_OP, 'id', COALESCE(NEW.id, OLD.id))::text);
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_task_notify ON tasks;
CREATE TRIGGER trg_task_notify AFTER INSERT OR UPDATE OR DELETE ON tasks
    FOR EACH ROW EXECUTE FUNCTION notify_task_change();

CREATE OR REPLACE FUNCTION notify_activity() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('jarvis_activity', json_build_object('table', TG_TABLE_NAME, 'op', TG_OP, 'id', NEW.id)::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_activity_tool_calls ON agent_tool_calls;
CREATE TRIGGER trg_activity_tool_calls AFTER INSERT ON agent_tool_calls
    FOR EACH ROW EXECUTE FUNCTION notify_activity();

DROP TRIGGER IF EXISTS trg_activity_observations ON cloud_observations;
CREATE TRIGGER trg_activity_observations AFTER INSERT ON cloud_observations
    FOR EACH ROW EXECUTE FUNCTION notify_activity();

-- ── System Metrics History ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS system_metrics (
    id          BIGSERIAL PRIMARY KEY,
    cpu_pct     REAL NOT NULL,
    mem_pct     REAL NOT NULL,
    disk_pct    REAL NOT NULL,
    load_1m     REAL NOT NULL,
    mem_used_mb INTEGER NOT NULL,
    mem_total_mb INTEGER NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_system_metrics_time ON system_metrics(recorded_at DESC);
`
