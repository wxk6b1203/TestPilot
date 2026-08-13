-- ============================================================================
-- TestPilot 全量 DDL —— PostgreSQL 13+
-- 依据 docs/data-model.md + Phase 8 实现（30 表）。主键为应用层 snowflake BIGINT（非自增）。
-- 约定：
--   * 文档型列一律 JSONB，内容为 types.proto 的 JSON 表示
--   * 软删实体带 deleted_at，查询统一 WHERE deleted_at IS NULL
--   * variables.environment_id = 0 表示项目级（哨兵值，故无 FK）
--   * test_plan_items.ref_id 为多态引用（case/suite），无 FK
--   * "order" 为保留字，全表加双引号
-- ============================================================================

-- ---- 租户与访问控制 ----

CREATE TABLE tenants (
    id          BIGINT PRIMARY KEY,
    name        VARCHAR(128) NOT NULL,
    status      SMALLINT     NOT NULL DEFAULT 1,          -- active/suspended
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            BIGINT PRIMARY KEY,
    username      VARCHAR(64)  NOT NULL UNIQUE,           -- 登录名
    email         VARCHAR(255) NOT NULL DEFAULT '',       -- 外部身份映射键
    password_hash VARCHAR(255),                           -- 本地账号；外部用户为空
    display_name  VARCHAR(128) NOT NULL DEFAULT '',
    status        SMALLINT     NOT NULL DEFAULT 1,        -- active/disabled
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_email ON users (email);

CREATE TABLE identity_providers (
    id            BIGINT PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL REFERENCES tenants (id),  -- 登录后落脚的租户
    name          VARCHAR(128) NOT NULL,
    type          VARCHAR(16)  NOT NULL,                          -- oidc
    issuer        TEXT         NOT NULL,                          -- OIDC issuer（discovery 入口）
    client_id     VARCHAR(128) NOT NULL,
    client_secret VARCHAR(255) NOT NULL DEFAULT '',
    enabled       BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_idp_tenant ON identity_providers (tenant_id);

CREATE TABLE tenant_members (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT    NOT NULL REFERENCES tenants (id),
    user_id    BIGINT    NOT NULL REFERENCES users (id),
    role       SMALLINT  NOT NULL,                        -- 1=owner 2=admin 3=member 4=viewer
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_tenant_user UNIQUE (tenant_id, user_id)
);
CREATE INDEX idx_tm_user ON tenant_members (user_id);

CREATE TABLE api_tokens (
    id           BIGINT PRIMARY KEY,
    tenant_id    BIGINT       NOT NULL REFERENCES tenants (id),
    user_id      BIGINT       NOT NULL REFERENCES users (id),  -- 颁发者
    name         VARCHAR(128) NOT NULL,
    token_hash   VARCHAR(255) NOT NULL,                        -- 仅存哈希
    scopes       JSONB        NOT NULL DEFAULT '[]',
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ
);
CREATE INDEX idx_api_tokens_tenant ON api_tokens (tenant_id);

-- ---- 项目 / 环境 / 变量 / 证书 ----

CREATE TABLE projects (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL REFERENCES tenants (id),
    name        VARCHAR(128) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    config      JSONB        NOT NULL DEFAULT '{}',       -- 项目级配置（默认超时/并发）
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX idx_projects_tenant ON projects (tenant_id) WHERE deleted_at IS NULL;

CREATE TABLE environments (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL REFERENCES tenants (id),
    project_id  BIGINT       NOT NULL REFERENCES projects (id),
    icon        VARCHAR(64)  NOT NULL DEFAULT '',
    name        VARCHAR(128) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    base_url    VARCHAR(1024) NOT NULL DEFAULT ''
);
CREATE INDEX idx_env_tp ON environments (tenant_id, project_id);

CREATE TABLE variables (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT       NOT NULL REFERENCES tenants (id),
    project_id     BIGINT       NOT NULL REFERENCES projects (id),
    environment_id BIGINT       NOT NULL DEFAULT 0,       -- 0 = 项目级（哨兵，无 FK）
    scope          SMALLINT     NOT NULL DEFAULT 1,       -- project/environment
    category       SMALLINT     NOT NULL DEFAULT 1,       -- header/cookie/query/body/custom
    key            VARCHAR(255) NOT NULL,
    value          TEXT         NOT NULL DEFAULT '',      -- 非敏感明文
    sensitive      BOOLEAN      NOT NULL DEFAULT FALSE,
    secret_ref     VARCHAR(512),                          -- vault://tenant/{tid}/... 或 tink 引用
    description    TEXT         NOT NULL DEFAULT '',
    CONSTRAINT uq_variable UNIQUE (project_id, environment_id, category, key)
);
CREATE INDEX idx_variables_tp ON variables (tenant_id, project_id);

CREATE TABLE certificates (
    id                  BIGINT PRIMARY KEY,
    tenant_id           BIGINT       NOT NULL REFERENCES tenants (id),
    project_id          BIGINT       NOT NULL REFERENCES projects (id),
    name                VARCHAR(128) NOT NULL,
    description         TEXT         NOT NULL DEFAULT '',
    type                VARCHAR(16)  NOT NULL,            -- pem/p12
    cert_ref            VARCHAR(512) NOT NULL,            -- artifact 引用或密文
    key_ref             VARCHAR(512) NOT NULL DEFAULT '',
    password_secret_ref VARCHAR(512)
);
CREATE INDEX idx_certificates_tp ON certificates (tenant_id, project_id);

-- ---- 接口 ----

CREATE TABLE http_apis (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT        NOT NULL REFERENCES tenants (id),
    project_id     BIGINT        NOT NULL REFERENCES projects (id),
    method         SMALLINT      NOT NULL,                -- HttpMethod
    uri            VARCHAR(1024) NOT NULL,                -- 可含 {{var}}
    params         JSONB,                                 -- KeyValue[]
    body           JSONB,                                 -- BodySpec
    headers        JSONB,                                 -- KeyValue[]
    cookies        JSONB,                                 -- CookieParam[]
    pre_scripts    JSONB,                                 -- Script[]
    post_scripts   JSONB,                                 -- Script[]
    settings       JSONB,                                 -- ApiSettings
    certificate_id BIGINT REFERENCES certificates (id),
    created_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);
CREATE INDEX idx_http_apis_tp ON http_apis (tenant_id, project_id) WHERE deleted_at IS NULL;

CREATE TABLE grpc_apis (
    id              BIGINT PRIMARY KEY,
    tenant_id       BIGINT        NOT NULL REFERENCES tenants (id),
    project_id      BIGINT        NOT NULL REFERENCES projects (id),
    proto_ref       VARCHAR(255)  NOT NULL DEFAULT '',    -- proto_file id 或 reflection
    full_service    VARCHAR(255)  NOT NULL,               -- package.Service
    method          VARCHAR(128)  NOT NULL,
    request_message JSONB,                                -- JSON 表示
    metadata        JSONB,                                -- KeyValue[]
    deadline_ms     INT           NOT NULL DEFAULT 0,
    tls_settings    JSONB,
    pre_scripts     JSONB,
    post_scripts    JSONB,
    certificate_id  BIGINT REFERENCES certificates (id),
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE INDEX idx_grpc_apis_tp ON grpc_apis (tenant_id, project_id) WHERE deleted_at IS NULL;

CREATE TABLE proto_files (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL REFERENCES tenants (id),
    project_id  BIGINT       NOT NULL REFERENCES projects (id),
    filename    VARCHAR(255) NOT NULL,
    content_ref VARCHAR(512) NOT NULL,                    -- artifact 引用（proto 源文件）
    imports     JSONB,                                    -- 依赖的 proto_file id 列表
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_proto_files_tp ON proto_files (tenant_id, project_id);

-- ---- 目录树 ----

CREATE TABLE tree_nodes (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT       NOT NULL REFERENCES tenants (id),
    project_id BIGINT       NOT NULL REFERENCES projects (id),
    parent_id  BIGINT       REFERENCES tree_nodes (id),   -- NULL = 根
    node_type  SMALLINT     NOT NULL,                     -- folder/http_api/grpc_api/test_case/test_suite/test_plan
    ref_id     BIGINT,                                    -- 指向实体；folder 为空
    name       VARCHAR(255) NOT NULL,
    icon       VARCHAR(64)  NOT NULL DEFAULT '',
    "order"    INT          NOT NULL DEFAULT 0,
    path       VARCHAR(2048) NOT NULL DEFAULT ''          -- 物化路径，子树查询
);
CREATE INDEX idx_tree_tp ON tree_nodes (tenant_id, project_id);
CREATE INDEX idx_tree_path ON tree_nodes (project_id, path);

-- ---- 测试用例 / 套件 / 计划 ----

CREATE TABLE test_cases (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL REFERENCES tenants (id),
    project_id  BIGINT       NOT NULL REFERENCES projects (id),
    type        SMALLINT     NOT NULL,                    -- declarative/lowcode
    name        VARCHAR(255) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    definition  JSONB,                                    -- declarative: steps[]；lowcode: script/entry/parameters
    tags        JSONB,                                    -- string[]
    created_by  SMALLINT     NOT NULL DEFAULT 1,          -- human/copilot
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX idx_test_cases_tp ON test_cases (tenant_id, project_id) WHERE deleted_at IS NULL;

CREATE TABLE test_suites (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL REFERENCES tenants (id),
    project_id  BIGINT       NOT NULL REFERENCES projects (id),
    name        VARCHAR(255) NOT NULL,
    description TEXT         NOT NULL DEFAULT ''
);
CREATE INDEX idx_test_suites_tp ON test_suites (tenant_id, project_id);

CREATE TABLE test_suite_items (
    id       BIGINT PRIMARY KEY,
    suite_id BIGINT NOT NULL REFERENCES test_suites (id),
    case_id  BIGINT NOT NULL REFERENCES test_cases (id),
    "order"  INT    NOT NULL DEFAULT 0
);
CREATE INDEX idx_suite_items ON test_suite_items (suite_id, "order");

CREATE TABLE scripts (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL REFERENCES tenants (id),
    project_id  BIGINT       NOT NULL REFERENCES projects (id),
    name        VARCHAR(255) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    language    VARCHAR(32)  NOT NULL DEFAULT 'python',
    content     TEXT         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX idx_scripts_tp ON scripts (tenant_id, project_id);

CREATE TABLE test_plans (
    id              BIGINT PRIMARY KEY,
    tenant_id       BIGINT       NOT NULL REFERENCES tenants (id),
    project_id      BIGINT       NOT NULL REFERENCES projects (id),
    env_id          BIGINT       NOT NULL REFERENCES environments (id),
    name            VARCHAR(255) NOT NULL,
    concurrency     INT          NOT NULL DEFAULT 1,      -- 用例间并发度
    retry_on_failure BOOLEAN     NOT NULL DEFAULT FALSE,
    overlap_policy  SMALLINT     NOT NULL DEFAULT 1,      -- skip/queue/run
    schedule_cron   VARCHAR(64),                          -- NULL = 手动
    timeout_ms      INT          NOT NULL DEFAULT 300000,
    notifications   JSONB,                                -- NotificationRule[]
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE INDEX idx_test_plans_tp ON test_plans (tenant_id, project_id) WHERE deleted_at IS NULL;

CREATE TABLE test_plan_items (
    id              BIGINT PRIMARY KEY,
    plan_id         BIGINT   NOT NULL REFERENCES test_plans (id),
    ref_type        SMALLINT NOT NULL,                    -- case/suite
    ref_id          BIGINT   NOT NULL,                    -- case_id 或 suite_id（多态，无 FK）
    enabled         BOOLEAN  NOT NULL DEFAULT TRUE,
    param_overrides JSONB,
    "order"         INT      NOT NULL DEFAULT 0
);
CREATE INDEX idx_plan_items ON test_plan_items (plan_id, "order");

-- ---- 运行结果 ----

CREATE TABLE test_runs (
    id           BIGINT PRIMARY KEY,
    tenant_id    BIGINT      NOT NULL REFERENCES tenants (id),
    plan_id      BIGINT      NOT NULL REFERENCES test_plans (id),
    env_id       BIGINT      NOT NULL DEFAULT 0,
    status       SMALLINT    NOT NULL,                    -- running/passed/failed/aborted/timeout
    trigger      SMALLINT    NOT NULL,                    -- manual/scheduled/ci
    triggered_by VARCHAR(128) NOT NULL DEFAULT '',        -- user_id/token/scheduler
    summary      JSONB,                                   -- RunSummary
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);
CREATE INDEX idx_runs_tp ON test_runs (tenant_id, plan_id);
CREATE INDEX idx_runs_status ON test_runs (tenant_id, status);
CREATE INDEX idx_runs_trend ON test_runs (tenant_id, plan_id, started_at);  -- 趋势报表

CREATE TABLE test_case_results (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT NOT NULL REFERENCES tenants (id),
    run_id      BIGINT NOT NULL REFERENCES test_runs (id),
    case_id     BIGINT NOT NULL,
    status      SMALLINT NOT NULL,
    duration_ms INT      NOT NULL DEFAULT 0,
    error       TEXT
);
CREATE INDEX idx_case_results_tr ON test_case_results (tenant_id, run_id);

CREATE TABLE test_step_results (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT       NOT NULL REFERENCES tenants (id),
    case_result_id BIGINT       NOT NULL REFERENCES test_case_results (id),
    step_path      VARCHAR(255) NOT NULL,                 -- 点路径定址（如 3.then.1）
    status         SMALLINT     NOT NULL,
    duration_ms    INT          NOT NULL DEFAULT 0,
    request        JSONB,                                 -- 请求快照（截断）
    response       JSONB,                                 -- 响应快照（截断）
    assertions     JSONB,                                 -- AssertionResult[]
    logs           JSONB                                  -- string[]
);
CREATE INDEX idx_step_results_tcr ON test_step_results (tenant_id, case_result_id);

CREATE TABLE artifacts (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT       NOT NULL REFERENCES tenants (id),
    run_id         BIGINT       REFERENCES test_runs (id),  -- NULL = 非运行产物（proto/证书）
    step_result_id BIGINT       REFERENCES test_step_results (id),  -- 精确归属（可空）
    kind           SMALLINT     NOT NULL,                   -- screenshot/video/trace/har/download/log/proto/cert
    uri            VARCHAR(1024) NOT NULL,                  -- 存储位置（S3/本地 FS）
    size           BIGINT       NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_artifacts_tr ON artifacts (tenant_id, run_id);

-- ---- 压力测试 ----

CREATE TABLE stress_test_plans (
    id                  BIGINT PRIMARY KEY,
    tenant_id           BIGINT      NOT NULL REFERENCES tenants (id),
    project_id          BIGINT      NOT NULL REFERENCES projects (id),
    env_id              BIGINT      NOT NULL REFERENCES environments (id),
    target_type         SMALLINT    NOT NULL,             -- api/behavior_case
    target_id           BIGINT      NOT NULL,             -- api_id 或低代码行为脚本 case_id
    load_profile        JSONB       NOT NULL,             -- LoadProfile（ramp/duration/concurrency_per_worker）
    worker_count        INT         NOT NULL DEFAULT 1,
    metrics_interval_ms INT         NOT NULL DEFAULT 1000,
    schedule_cron       VARCHAR(64),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ
);
CREATE INDEX idx_stress_plans_tp ON stress_test_plans (tenant_id, project_id) WHERE deleted_at IS NULL;

CREATE TABLE stress_runs (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT NOT NULL REFERENCES tenants (id),
    stress_plan_id BIGINT NOT NULL REFERENCES stress_test_plans (id),
    env_id         BIGINT NOT NULL DEFAULT 0,
    status         SMALLINT NOT NULL,
    summary        JSONB,                                 -- 聚合指标摘要（时序明细在 VictoriaMetrics）
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ
);
CREATE INDEX idx_stress_runs ON stress_runs (tenant_id, stress_plan_id);

-- 压测时序指标点：dev/单机内嵌存储。生产部署切换 VictoriaMetrics（查询层不变，本表闲置）。
CREATE TABLE stress_metric_points (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT      NOT NULL REFERENCES tenants (id),
    stress_run_id  BIGINT      NOT NULL REFERENCES stress_runs (id),
    ts             TIMESTAMPTZ NOT NULL,
    rps            DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_p50_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_p95_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_p99_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    error_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,
    concurrency    INT          NOT NULL DEFAULT 0
);
CREATE INDEX idx_stress_metrics_run ON stress_metric_points (tenant_id, stress_run_id);

-- ---- Copilot ----

CREATE TABLE copilot_sessions (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT       NOT NULL REFERENCES tenants (id),
    user_id    BIGINT       NOT NULL REFERENCES users (id),
    title      VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_copilot_sessions_tu ON copilot_sessions (tenant_id, user_id);

CREATE TABLE copilot_messages (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT  NOT NULL REFERENCES tenants (id),
    session_id BIGINT  NOT NULL REFERENCES copilot_sessions (id),
    role       SMALLINT NOT NULL,                         -- user/assistant/tool
    content    TEXT    NOT NULL DEFAULT '',
    tool_calls JSONB,                                     -- 工具调用与结果
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_copilot_messages_ts ON copilot_messages (tenant_id, session_id);

-- ---- 审计 / 配额 ----

CREATE TABLE tenant_settings (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT NOT NULL REFERENCES tenants (id),
    key        VARCHAR(64) NOT NULL,
    value      TEXT    NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, key)
);
CREATE TABLE audit_logs (
    id            BIGINT PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL REFERENCES tenants (id),
    actor         SMALLINT     NOT NULL,                  -- human/copilot
    actor_id      VARCHAR(128) NOT NULL DEFAULT '',       -- user_id 或 copilot session
    action        VARCHAR(64)  NOT NULL,                  -- create/update/delete/run/secret_read/...
    resource_type VARCHAR(64)  NOT NULL,
    resource_id   VARCHAR(64)  NOT NULL DEFAULT '',
    approved_by   VARCHAR(128),                           -- HITL 审批人（Copilot 写操作）
    detail        JSONB,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_tc ON audit_logs (tenant_id, created_at);

-- 注意：表名为单数 tenant_quota（ORM 命名特例）。用量实时从事实表计算，不落库。
CREATE TABLE tenant_quota (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT      NOT NULL REFERENCES tenants (id),
    metric     VARCHAR(32) NOT NULL,                      -- concurrent_runs/worker_slots/artifact_bytes/monthly_runs/ai_calls
    "limit"    BIGINT      NOT NULL DEFAULT 0,            -- 0 = 不限（保留字，需引号）
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_quota UNIQUE (tenant_id, metric)
);

-- ---- 定时调度 / 通知（Phase 8） ----

CREATE TABLE schedules (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT      NOT NULL REFERENCES tenants (id),
    plan_id        BIGINT      NOT NULL REFERENCES test_plans (id),
    env_id         BIGINT      NOT NULL REFERENCES environments (id),
    name           VARCHAR(128) NOT NULL DEFAULT '',
    cron_expr      VARCHAR(64)  NOT NULL,                 -- 标准 5 段 cron（分 时 日 月 周）
    overlap_policy SMALLINT     NOT NULL DEFAULT 1,       -- 1=跳过（上次未结束） 2=允许并发
    enabled        BOOLEAN      NOT NULL DEFAULT TRUE,
    last_run_at    TIMESTAMPTZ,
    next_run_at    TIMESTAMPTZ,                           -- misfire 检测用（启动时落后 >2min 补跑）
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_schedules_tenant ON schedules (tenant_id);

CREATE TABLE notification_channels (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT       NOT NULL REFERENCES tenants (id),
    name       VARCHAR(128) NOT NULL,
    type       SMALLINT     NOT NULL,                     -- 1=webhook 2=dingtalk 3=feishu
    url        TEXT         NOT NULL,
    secret     VARCHAR(255) NOT NULL DEFAULT '',          -- 钉钉/飞书签名密钥
    events     VARCHAR(255) NOT NULL DEFAULT '',          -- 逗号分隔：run_finished,stress_finished
    enabled    BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_notify_tenant ON notification_channels (tenant_id);
