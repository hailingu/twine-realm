-- 业务部署独立迁移历史；不可变目标与同库回执先于外部账号、迁移和容器副作用。
CREATE TABLE deployment_instances (
    project_id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    generation BIGINT UNSIGNED NOT NULL DEFAULT 0,
    sequence BIGINT UNSIGNED NOT NULL DEFAULT 0,
    current_operation_id CHAR(36) CHARACTER SET ascii NOT NULL DEFAULT '',
    ever_ready BOOLEAN NOT NULL DEFAULT FALSE,
    observation_json JSON NULL,
    observation_code VARCHAR(96) NOT NULL DEFAULT '',
    last_attempt_at DATETIME(6) NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id),
    FOREIGN KEY (service_id) REFERENCES services(id)
);
CREATE TABLE deployment_targets (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    payload JSON NOT NULL,
    UNIQUE KEY deployment_target_generation(project_id,generation),
    FOREIGN KEY (project_id) REFERENCES deployment_instances(project_id)
);
CREATE TABLE deployment_operations (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    target_id CHAR(36) CHARACTER SET ascii NOT NULL,
    state VARCHAR(32) NOT NULL CHECK (state IN ('accepted','executing','succeeded','failed','needs_attention')),
    version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    execution BIGINT UNSIGNED NOT NULL DEFAULT 0,
    lease_until DATETIME(6) NULL,
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    KEY deployment_queue(state,lease_until),
    FOREIGN KEY (project_id) REFERENCES deployment_instances(project_id),
    FOREIGN KEY (target_id) REFERENCES deployment_targets(id)
);
CREATE TABLE deployment_receipts (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    intent_hash BINARY(32) NOT NULL,
    response JSON NULL,
    created_at DATETIME(6) NOT NULL
);
CREATE TABLE deployment_events (
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    sequence BIGINT UNSIGNED NOT NULL,
    operation_id CHAR(36) CHARACTER SET ascii NOT NULL,
    kind VARCHAR(32) NOT NULL,
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY(project_id,sequence),
    FOREIGN KEY (project_id) REFERENCES deployment_instances(project_id),
    FOREIGN KEY (operation_id) REFERENCES deployment_operations(id)
);
