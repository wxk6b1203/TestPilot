-- ============================================================================
-- TestPilot 全量 DDL —— MySQL 8.0+（InnoDB / utf8mb4）
-- 依据 docs/data-model.md + Phase 8 实现（30 表）。主键为应用层 snowflake BIGINT（非自增）。
-- 与 PostgreSQL 版差异：
--   * TIMESTAMPTZ → DATETIME(3)；JSONB → JSON
--   * MySQL 不支持部分索引（WHERE deleted_at IS NULL），用普通索引代替
--   * 保留字 order 用反引号
-- ============================================================================

-- ---- 租户与访问控制 ----

CREATE TABLE tenants (
    id          BIGINT       PRIMARY KEY,
    name        VARCHAR(128) NOT NULL,
    status      SMALLINT     NOT NULL DEFAULT 1 COMMENT '1=active 2=suspended',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE users (
    id            BIGINT       PRIMARY KEY,
    username      VARCHAR(64)  NOT NULL,
    email         VARCHAR(255) NOT NULL DEFAULT '' COMMENT '外部身份映射键',
    password_hash VARCHAR(255) NULL COMMENT '本地账号；外部用户为空',
    display_name  VARCHAR(128) NOT NULL DEFAULT '',
    status        SMALLINT     NOT NULL DEFAULT 1,
    created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY uq_users_username (username),
    KEY idx_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE identity_providers (
    id            BIGINT PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL COMMENT '登录后落脚的租户',
    name          VARCHAR(128) NOT NULL,
    type          VARCHAR(16)  NOT NULL COMMENT 'oidc',
    issuer        TEXT         NOT NULL COMMENT 'OIDC issuer（discovery 入口）',
    client_id     VARCHAR(128) NOT NULL,
    client_secret VARCHAR(255) NOT NULL DEFAULT '',
    enabled       BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_idp_tenant (tenant_id),
    CONSTRAINT fk_idp_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE tenant_members (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT      NOT NULL,
    user_id    BIGINT      NOT NULL,
    role       SMALLINT    NOT NULL COMMENT '1=owner 2=admin 3=member 4=viewer',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY uq_tenant_user (tenant_id, user_id),
    KEY idx_tm_user (user_id),
    CONSTRAINT fk_tm_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_tm_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE api_tokens (
    id           BIGINT PRIMARY KEY,
    tenant_id    BIGINT       NOT NULL,
    user_id      BIGINT       NOT NULL COMMENT '颁发者',
    name         VARCHAR(128) NOT NULL,
    token_hash   VARCHAR(255) NOT NULL COMMENT '仅存哈希',
    scopes       JSON         NOT NULL,
    expires_at   DATETIME(3)  NULL,
    last_used_at DATETIME(3)  NULL,
    KEY idx_api_tokens_tenant (tenant_id),
    CONSTRAINT fk_at_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_at_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 项目 / 环境 / 变量 / 证书 ----

CREATE TABLE projects (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL,
    name        VARCHAR(128) NOT NULL,
    description TEXT         NOT NULL,
    config      JSON         NULL COMMENT '项目级配置（默认超时/并发）',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at  DATETIME(3)  NULL,
    KEY idx_projects_tenant (tenant_id),
    CONSTRAINT fk_projects_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE environments (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT        NOT NULL,
    project_id  BIGINT        NOT NULL,
    icon        VARCHAR(64)   NOT NULL DEFAULT '',
    name        VARCHAR(128)  NOT NULL,
    description TEXT          NOT NULL,
    base_url    VARCHAR(1024) NOT NULL DEFAULT '',
    KEY idx_env_tp (tenant_id, project_id),
    CONSTRAINT fk_env_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_env_project FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE variables (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT       NOT NULL,
    project_id     BIGINT       NOT NULL,
    environment_id BIGINT       NOT NULL DEFAULT 0 COMMENT '0 = 项目级（哨兵值，故无 FK）',
    scope          SMALLINT     NOT NULL DEFAULT 1 COMMENT '1=project 2=environment',
    category       SMALLINT     NOT NULL DEFAULT 1 COMMENT 'header/cookie/query/body/custom',
    `key`          VARCHAR(255) NOT NULL,
    value          TEXT         NOT NULL COMMENT '非敏感明文',
    sensitive      BOOLEAN      NOT NULL DEFAULT FALSE,
    secret_ref     VARCHAR(512) NULL COMMENT 'vault://tenant/{tid}/... 或 tink 引用',
    description    TEXT         NOT NULL,
    UNIQUE KEY uq_variable (project_id, environment_id, category, `key`),
    KEY idx_variables_tp (tenant_id, project_id),
    CONSTRAINT fk_var_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_var_project FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE certificates (
    id                  BIGINT PRIMARY KEY,
    tenant_id           BIGINT       NOT NULL,
    project_id          BIGINT       NOT NULL,
    name                VARCHAR(128) NOT NULL,
    description         TEXT         NOT NULL,
    type                VARCHAR(16)  NOT NULL COMMENT 'pem/p12',
    cert_ref            VARCHAR(512) NOT NULL COMMENT 'artifact 引用或密文',
    key_ref             VARCHAR(512) NOT NULL DEFAULT '',
    password_secret_ref VARCHAR(512) NULL,
    KEY idx_certificates_tp (tenant_id, project_id),
    CONSTRAINT fk_cert_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_cert_project FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 接口 ----

CREATE TABLE http_apis (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT        NOT NULL,
    project_id     BIGINT        NOT NULL,
    method         SMALLINT      NOT NULL COMMENT 'HttpMethod',
    uri            VARCHAR(1024) NOT NULL COMMENT '可含 {{var}}',
    params         JSON          NULL COMMENT 'KeyValue[]',
    body           JSON          NULL COMMENT 'BodySpec',
    headers        JSON          NULL COMMENT 'KeyValue[]',
    cookies        JSON          NULL COMMENT 'CookieParam[]',
    pre_scripts    JSON          NULL COMMENT 'Script[]',
    post_scripts   JSON          NULL COMMENT 'Script[]',
    settings       JSON          NULL COMMENT 'ApiSettings',
    certificate_id BIGINT        NULL,
    created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at     DATETIME(3)   NULL,
    KEY idx_http_apis_tp (tenant_id, project_id),
    CONSTRAINT fk_ha_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_ha_project FOREIGN KEY (project_id) REFERENCES projects (id),
    CONSTRAINT fk_ha_cert FOREIGN KEY (certificate_id) REFERENCES certificates (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE grpc_apis (
    id              BIGINT PRIMARY KEY,
    tenant_id       BIGINT        NOT NULL,
    project_id      BIGINT        NOT NULL,
    proto_ref       VARCHAR(255)  NOT NULL DEFAULT '' COMMENT 'proto_file id 或 reflection',
    full_service    VARCHAR(255)  NOT NULL COMMENT 'package.Service',
    method          VARCHAR(128)  NOT NULL,
    request_message JSON          NULL,
    metadata        JSON          NULL COMMENT 'KeyValue[]',
    deadline_ms     INT           NOT NULL DEFAULT 0,
    tls_settings    JSON          NULL,
    pre_scripts     JSON          NULL,
    post_scripts    JSON          NULL,
    certificate_id  BIGINT        NULL,
    created_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at      DATETIME(3)   NULL,
    KEY idx_grpc_apis_tp (tenant_id, project_id),
    CONSTRAINT fk_ga_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_ga_project FOREIGN KEY (project_id) REFERENCES projects (id),
    CONSTRAINT fk_ga_cert FOREIGN KEY (certificate_id) REFERENCES certificates (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE proto_files (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL,
    project_id  BIGINT       NOT NULL,
    filename    VARCHAR(255) NOT NULL,
    content_ref VARCHAR(512) NOT NULL COMMENT 'artifact 引用（proto 源文件）',
    imports     JSON         NULL COMMENT '依赖的 proto_file id 列表',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_proto_files_tp (tenant_id, project_id),
    CONSTRAINT fk_pf_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_pf_project FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 目录树 ----

CREATE TABLE tree_nodes (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT        NOT NULL,
    project_id BIGINT        NOT NULL,
    parent_id  BIGINT        NULL COMMENT 'NULL = 根',
    node_type  SMALLINT      NOT NULL COMMENT 'folder/http_api/grpc_api/test_case/test_suite/test_plan',
    ref_id     BIGINT        NULL COMMENT '指向实体；folder 为空',
    name       VARCHAR(255)  NOT NULL,
    icon       VARCHAR(64)   NOT NULL DEFAULT '',
    `order`    INT           NOT NULL DEFAULT 0,
    path       VARCHAR(2048) NOT NULL DEFAULT '' COMMENT '物化路径',
    KEY idx_tree_tp (tenant_id, project_id),
    KEY idx_tree_path (project_id, path(255)),
    CONSTRAINT fk_tn_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_tn_project FOREIGN KEY (project_id) REFERENCES projects (id),
    CONSTRAINT fk_tn_parent FOREIGN KEY (parent_id) REFERENCES tree_nodes (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 测试用例 / 套件 / 计划 ----

CREATE TABLE test_cases (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL,
    project_id  BIGINT       NOT NULL,
    type        SMALLINT     NOT NULL COMMENT '1=declarative 2=lowcode',
    name        VARCHAR(255) NOT NULL,
    description TEXT         NOT NULL,
    definition  JSON         NULL COMMENT 'declarative: steps[]；lowcode: script/entry/parameters',
    tags        JSON         NULL COMMENT 'string[]',
    created_by  SMALLINT     NOT NULL DEFAULT 1 COMMENT '1=human 2=copilot',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at  DATETIME(3)  NULL,
    KEY idx_test_cases_tp (tenant_id, project_id),
    CONSTRAINT fk_tc_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_tc_project FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE test_suites (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL,
    project_id  BIGINT       NOT NULL,
    name        VARCHAR(255) NOT NULL,
    description TEXT         NOT NULL,
    KEY idx_test_suites_tp (tenant_id, project_id),
    CONSTRAINT fk_ts_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_ts_project FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE test_suite_items (
    id       BIGINT PRIMARY KEY,
    suite_id BIGINT NOT NULL,
    case_id  BIGINT NOT NULL,
    `order`  INT    NOT NULL DEFAULT 0,
    KEY idx_suite_items (suite_id, `order`),
    CONSTRAINT fk_tsi_suite FOREIGN KEY (suite_id) REFERENCES test_suites (id),
    CONSTRAINT fk_tsi_case FOREIGN KEY (case_id) REFERENCES test_cases (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE scripts (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT       NOT NULL,
    project_id  BIGINT       NOT NULL,
    name        VARCHAR(255) NOT NULL,
    description TEXT         NOT NULL,
    language    VARCHAR(32)  NOT NULL DEFAULT 'python',
    content     TEXT         NOT NULL,
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    deleted_at  DATETIME(3)  NULL,
    KEY idx_scripts_tp (tenant_id, project_id),
    CONSTRAINT fk_scripts_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_scripts_project FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE test_plans (
    id               BIGINT PRIMARY KEY,
    tenant_id        BIGINT       NOT NULL,
    project_id       BIGINT       NOT NULL,
    env_id           BIGINT       NOT NULL,
    name             VARCHAR(255) NOT NULL,
    concurrency      INT          NOT NULL DEFAULT 1,
    retry_on_failure BOOLEAN      NOT NULL DEFAULT FALSE,
    overlap_policy   SMALLINT     NOT NULL DEFAULT 1 COMMENT '1=skip 2=queue 3=run',
    schedule_cron    VARCHAR(64)  NULL COMMENT 'NULL = 手动',
    timeout_ms       INT          NOT NULL DEFAULT 300000,
    notifications    JSON         NULL COMMENT 'NotificationRule[]',
    created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at       DATETIME(3)  NULL,
    KEY idx_test_plans_tp (tenant_id, project_id),
    CONSTRAINT fk_tp_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_tp_project FOREIGN KEY (project_id) REFERENCES projects (id),
    CONSTRAINT fk_tp_env FOREIGN KEY (env_id) REFERENCES environments (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE test_plan_items (
    id              BIGINT PRIMARY KEY,
    plan_id         BIGINT   NOT NULL,
    ref_type        SMALLINT NOT NULL COMMENT '1=case 2=suite',
    ref_id          BIGINT   NOT NULL COMMENT 'case_id 或 suite_id（多态，无 FK）',
    enabled         BOOLEAN  NOT NULL DEFAULT TRUE,
    param_overrides JSON     NULL,
    `order`         INT      NOT NULL DEFAULT 0,
    KEY idx_plan_items (plan_id, `order`),
    CONSTRAINT fk_tpi_plan FOREIGN KEY (plan_id) REFERENCES test_plans (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 运行结果 ----

CREATE TABLE test_runs (
    id           BIGINT PRIMARY KEY,
    tenant_id    BIGINT       NOT NULL,
    plan_id      BIGINT       NOT NULL,
    env_id       BIGINT       NOT NULL DEFAULT 0,
    status       SMALLINT     NOT NULL COMMENT '1=running 2=passed 3=failed 4=aborted 5=timeout',
    `trigger`    SMALLINT     NOT NULL COMMENT '1=manual 2=scheduled 3=ci',
    triggered_by VARCHAR(128) NOT NULL DEFAULT '',
    summary      JSON         NULL COMMENT 'RunSummary',
    started_at   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    finished_at  DATETIME(3)  NULL,
    KEY idx_runs_tp (tenant_id, plan_id),
    KEY idx_runs_status (tenant_id, status),
    KEY idx_runs_trend (tenant_id, plan_id, started_at),
    CONSTRAINT fk_tr_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_tr_plan FOREIGN KEY (plan_id) REFERENCES test_plans (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE test_case_results (
    id          BIGINT PRIMARY KEY,
    tenant_id   BIGINT   NOT NULL,
    run_id      BIGINT   NOT NULL,
    case_id     BIGINT   NOT NULL,
    status      SMALLINT NOT NULL,
    duration_ms INT      NOT NULL DEFAULT 0,
    error       TEXT     NULL,
    KEY idx_case_results_tr (tenant_id, run_id),
    CONSTRAINT fk_tcr_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_tcr_run FOREIGN KEY (run_id) REFERENCES test_runs (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE test_step_results (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT       NOT NULL,
    case_result_id BIGINT       NOT NULL,
    step_path      VARCHAR(255) NOT NULL COMMENT '点路径定址（如 3.then.1）',
    status         SMALLINT     NOT NULL,
    duration_ms    INT          NOT NULL DEFAULT 0,
    request        JSON         NULL COMMENT '请求快照（截断）',
    response       JSON         NULL COMMENT '响应快照（截断）',
    assertions     JSON         NULL COMMENT 'AssertionResult[]',
    logs           JSON         NULL COMMENT 'string[]',
    KEY idx_step_results_tcr (tenant_id, case_result_id),
    CONSTRAINT fk_tsr_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_tsr_cr FOREIGN KEY (case_result_id) REFERENCES test_case_results (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE artifacts (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT        NOT NULL,
    run_id         BIGINT        NULL COMMENT 'NULL = 非运行产物（proto/证书）',
    step_result_id BIGINT        NULL COMMENT '精确归属（可空）',
    kind           SMALLINT      NOT NULL COMMENT 'screenshot/video/trace/har/download/log/proto/cert',
    uri            VARCHAR(1024) NOT NULL COMMENT '存储位置（S3/本地 FS）',
    size           BIGINT        NOT NULL DEFAULT 0,
    created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_artifacts_tr (tenant_id, run_id),
    CONSTRAINT fk_art_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_art_run FOREIGN KEY (run_id) REFERENCES test_runs (id),
    CONSTRAINT fk_art_step FOREIGN KEY (step_result_id) REFERENCES test_step_results (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 压力测试 ----

CREATE TABLE stress_test_plans (
    id                  BIGINT PRIMARY KEY,
    tenant_id           BIGINT      NOT NULL,
    project_id          BIGINT      NOT NULL,
    env_id              BIGINT      NOT NULL,
    target_type         SMALLINT    NOT NULL COMMENT '1=api 2=behavior_case',
    target_id           BIGINT      NOT NULL COMMENT 'api_id 或低代码行为脚本 case_id',
    load_profile        JSON        NOT NULL COMMENT 'LoadProfile（ramp/duration/concurrency_per_worker）',
    worker_count        INT         NOT NULL DEFAULT 1,
    metrics_interval_ms INT         NOT NULL DEFAULT 1000,
    schedule_cron       VARCHAR(64) NULL,
    created_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at          DATETIME(3) NULL,
    KEY idx_stress_plans_tp (tenant_id, project_id),
    CONSTRAINT fk_stp_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_stp_project FOREIGN KEY (project_id) REFERENCES projects (id),
    CONSTRAINT fk_stp_env FOREIGN KEY (env_id) REFERENCES environments (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE stress_runs (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT NOT NULL,
    stress_plan_id BIGINT NOT NULL,
    env_id         BIGINT NOT NULL DEFAULT 0,
    status         SMALLINT NOT NULL,
    summary        JSON   NULL COMMENT '聚合指标摘要（时序明细在 VictoriaMetrics）',
    started_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    finished_at    DATETIME(3) NULL,
    KEY idx_stress_runs (tenant_id, stress_plan_id),
    CONSTRAINT fk_sr_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_sr_plan FOREIGN KEY (stress_plan_id) REFERENCES stress_test_plans (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 压测时序指标点：dev/单机内嵌存储。生产部署切换 VictoriaMetrics（查询层不变，本表闲置）。
CREATE TABLE stress_metric_points (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT NOT NULL,
    stress_run_id  BIGINT NOT NULL,
    ts             DATETIME(3) NOT NULL,
    rps            DOUBLE NOT NULL DEFAULT 0,
    latency_p50_ms DOUBLE NOT NULL DEFAULT 0,
    latency_p95_ms DOUBLE NOT NULL DEFAULT 0,
    latency_p99_ms DOUBLE NOT NULL DEFAULT 0,
    error_rate     DOUBLE NOT NULL DEFAULT 0,
    concurrency    INT NOT NULL DEFAULT 0,
    KEY idx_stress_metrics_run (tenant_id, stress_run_id),
    CONSTRAINT fk_smp_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_smp_run FOREIGN KEY (stress_run_id) REFERENCES stress_runs (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- Copilot ----

CREATE TABLE copilot_sessions (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT       NOT NULL,
    user_id    BIGINT       NOT NULL,
    title      VARCHAR(255) NOT NULL DEFAULT '',
    created_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_copilot_sessions_tu (tenant_id, user_id),
    CONSTRAINT fk_cs_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_cs_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE copilot_messages (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT   NOT NULL,
    session_id BIGINT   NOT NULL,
    role       SMALLINT NOT NULL COMMENT '1=user 2=assistant 3=tool',
    content    TEXT     NOT NULL,
    tool_calls JSON     NULL COMMENT '工具调用与结果',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_copilot_messages_ts (tenant_id, session_id),
    CONSTRAINT fk_cm_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_cm_session FOREIGN KEY (session_id) REFERENCES copilot_sessions (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 审计 / 配额 ----

CREATE TABLE tenant_settings (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT NOT NULL,
    `key`      VARCHAR(64) NOT NULL,
    value      TEXT    NOT NULL,
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY uq_tsetting_tk (tenant_id, `key`),
    CONSTRAINT fk_tsetting_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE audit_logs (
    id            BIGINT PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL,
    actor         SMALLINT     NOT NULL COMMENT '1=human 2=copilot',
    actor_id      VARCHAR(128) NOT NULL DEFAULT '',
    action        VARCHAR(64)  NOT NULL COMMENT 'create/update/delete/run/secret_read/...',
    resource_type VARCHAR(64)  NOT NULL,
    resource_id   VARCHAR(64)  NOT NULL DEFAULT '',
    approved_by   VARCHAR(128) NULL COMMENT 'HITL 审批人（Copilot 写操作）',
    detail        JSON         NULL,
    created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_audit_tc (tenant_id, created_at),
    CONSTRAINT fk_al_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 注意：表名为单数 tenant_quota（ORM 命名特例）。用量实时从事实表计算，不落库。
CREATE TABLE tenant_quota (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT      NOT NULL,
    metric     VARCHAR(32) NOT NULL COMMENT 'concurrent_runs/worker_slots/artifact_bytes/monthly_runs/ai_calls',
    `limit`    BIGINT      NOT NULL DEFAULT 0 COMMENT '0 = 不限（保留字，需反引号）',
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY uq_quota (tenant_id, metric),
    CONSTRAINT fk_tq_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---- 定时调度 / 通知（Phase 8） ----

CREATE TABLE schedules (
    id             BIGINT PRIMARY KEY,
    tenant_id      BIGINT       NOT NULL,
    plan_id        BIGINT       NOT NULL,
    env_id         BIGINT       NOT NULL,
    name           VARCHAR(128) NOT NULL DEFAULT '',
    cron_expr      VARCHAR(64)  NOT NULL COMMENT '标准 5 段 cron（分 时 日 月 周）',
    overlap_policy SMALLINT     NOT NULL DEFAULT 1 COMMENT '1=跳过（上次未结束） 2=允许并发',
    enabled        BOOLEAN      NOT NULL DEFAULT TRUE,
    last_run_at    DATETIME(3)  NULL,
    next_run_at    DATETIME(3)  NULL COMMENT 'misfire 检测用（启动时落后 >2min 补跑）',
    created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_schedules_tenant (tenant_id),
    CONSTRAINT fk_sc_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT fk_sc_plan FOREIGN KEY (plan_id) REFERENCES test_plans (id),
    CONSTRAINT fk_sc_env FOREIGN KEY (env_id) REFERENCES environments (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE notification_channels (
    id         BIGINT PRIMARY KEY,
    tenant_id  BIGINT       NOT NULL,
    name       VARCHAR(128) NOT NULL,
    type       SMALLINT     NOT NULL COMMENT '1=webhook 2=dingtalk 3=feishu',
    url        TEXT         NOT NULL,
    secret     VARCHAR(255) NOT NULL DEFAULT '' COMMENT '钉钉/飞书签名密钥',
    events     VARCHAR(255) NOT NULL DEFAULT '' COMMENT '逗号分隔：run_finished,stress_finished',
    enabled    BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_notify_tenant (tenant_id),
    CONSTRAINT fk_nc_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
