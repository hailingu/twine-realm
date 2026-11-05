-- 独立运行历史与核心迁移分开，目标、步骤与事件仍共用同一个元数据库事务。
CREATE TABLE runtime_installations (
    singleton TINYINT UNSIGNED PRIMARY KEY CHECK (singleton = 1),
    installation_id CHAR(36) CHARACTER SET ascii NOT NULL UNIQUE,
    daemon_id VARCHAR(128) NOT NULL
);
CREATE TABLE runtime_instances (
    project_id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    component_id CHAR(36) CHARACTER SET ascii NOT NULL,
    installation_id CHAR(36) CHARACTER SET ascii NOT NULL,
    resource_id CHAR(36) CHARACTER SET ascii NOT NULL UNIQUE,
    resource_json JSON NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    sequence BIGINT UNSIGNED NOT NULL DEFAULT 0,
    current_operation_id CHAR(36) CHARACTER SET ascii NOT NULL,
    volume_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    server_uuid CHAR(36) CHARACTER SET ascii NOT NULL DEFAULT '',
    observation_json JSON NULL,
    observation_code VARCHAR(80) NOT NULL DEFAULT '',
    last_attempt_at DATETIME(6) NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id),
    FOREIGN KEY (component_id) REFERENCES middlewares(id),
    FOREIGN KEY (installation_id) REFERENCES runtime_installations(installation_id)
);
CREATE TABLE runtime_targets (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    desired VARCHAR(16) NOT NULL CHECK (desired IN ('running','stopped')),
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    UNIQUE KEY runtime_target_generation(project_id,generation),
    FOREIGN KEY (project_id) REFERENCES runtime_instances(project_id)
);
CREATE TABLE runtime_operations (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    mutation_id CHAR(36) CHARACTER SET ascii NOT NULL UNIQUE,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    target_id CHAR(36) CHARACTER SET ascii NOT NULL,
    state VARCHAR(32) NOT NULL CHECK (state IN ('accepted','executing','waiting_retry','needs_attention','succeeded','failed','superseded')),
    version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    attempts INT UNSIGNED NOT NULL DEFAULT 0 CHECK (attempts <= 6),
    execution BIGINT UNSIGNED NOT NULL DEFAULT 0,
    lease_until DATETIME(6) NULL,
    next_attempt DATETIME(6) NOT NULL,
    code VARCHAR(80) NOT NULL DEFAULT '',
    message VARCHAR(256) NOT NULL DEFAULT '',
    retry_of CHAR(36) CHARACTER SET ascii NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    KEY runtime_queue(state,next_attempt,lease_until),
    FOREIGN KEY (project_id) REFERENCES runtime_instances(project_id),
    FOREIGN KEY (target_id) REFERENCES runtime_targets(id)
);
CREATE TABLE runtime_steps (
    operation_id CHAR(36) CHARACTER SET ascii NOT NULL,
    name VARCHAR(24) NOT NULL,
    state VARCHAR(24) NOT NULL CHECK (state IN ('pending','executing','unknown','succeeded','failed','skipped')),
    code VARCHAR(80) NOT NULL DEFAULT '',
    external_id VARCHAR(128) NOT NULL DEFAULT '',
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (operation_id,name),
    FOREIGN KEY (operation_id) REFERENCES runtime_operations(id)
);
CREATE TABLE runtime_receipts (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    intent_hash BINARY(32) NOT NULL,
    response JSON NULL,
    created_at DATETIME(6) NOT NULL
);
CREATE TABLE runtime_events (
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    sequence BIGINT UNSIGNED NOT NULL,
    operation_id CHAR(36) CHARACTER SET ascii NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    kind VARCHAR(32) NOT NULL,
    code VARCHAR(80) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (project_id,sequence),
    FOREIGN KEY (project_id) REFERENCES runtime_instances(project_id),
    FOREIGN KEY (operation_id) REFERENCES runtime_operations(id)
);
