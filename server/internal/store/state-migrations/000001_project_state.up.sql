CREATE TABLE project_state_heads (
    project_id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    sequence BIGINT UNSIGNED NOT NULL DEFAULT 0,
    replay_floor BIGINT UNSIGNED NOT NULL DEFAULT 0,
    FOREIGN KEY (project_id) REFERENCES projects(id)
) ENGINE=InnoDB;

CREATE TABLE project_state_events (
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    sequence BIGINT UNSIGNED NOT NULL,
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (project_id, sequence),
    KEY retention_window (project_id, created_at, sequence),
    FOREIGN KEY (project_id) REFERENCES project_state_heads(project_id)
) ENGINE=InnoDB;
