CREATE TABLE build_installations (
    singleton TINYINT UNSIGNED PRIMARY KEY,
    installation_id CHAR(36) CHARACTER SET ascii NOT NULL,
    environment_digest CHAR(71) CHARACTER SET ascii NOT NULL,
    CHECK (singleton = 1)
) ENGINE=InnoDB;

CREATE TABLE build_instances (
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    generation BIGINT UNSIGNED NOT NULL DEFAULT 0,
    current_operation_id CHAR(36) CHARACTER SET ascii NOT NULL DEFAULT '',
    candidate_operation_id CHAR(36) CHARACTER SET ascii NOT NULL DEFAULT '',
    PRIMARY KEY (project_id, service_id),
    FOREIGN KEY (project_id) REFERENCES projects(id),
    FOREIGN KEY (service_id) REFERENCES services(id)
) ENGINE=InnoDB;

CREATE TABLE build_targets (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    payload JSON NOT NULL,
    UNIQUE KEY build_generation (project_id, service_id, generation),
    FOREIGN KEY (project_id, service_id) REFERENCES build_instances(project_id, service_id)
) ENGINE=InnoDB;

CREATE TABLE build_operations (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    target_id CHAR(36) CHARACTER SET ascii NOT NULL UNIQUE,
    state VARCHAR(32) CHARACTER SET ascii NOT NULL,
    version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    execution BIGINT UNSIGNED NOT NULL DEFAULT 0,
    lease_until DATETIME(6) NULL,
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    KEY build_queue (state, lease_until, created_at),
    FOREIGN KEY (target_id) REFERENCES build_targets(id)
) ENGINE=InnoDB;

CREATE TABLE build_receipts (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    intent_hash BINARY(32) NOT NULL,
    response JSON NULL,
    created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;

CREATE TABLE build_events (
    operation_id CHAR(36) CHARACTER SET ascii NOT NULL,
    version BIGINT UNSIGNED NOT NULL,
    kind VARCHAR(32) CHARACTER SET ascii NOT NULL,
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (operation_id, version),
    FOREIGN KEY (operation_id) REFERENCES build_operations(id)
) ENGINE=InnoDB;
