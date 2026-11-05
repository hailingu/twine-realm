CREATE TABLE middlewares (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    definition_id VARCHAR(80) CHARACTER SET ascii NOT NULL,
    definition_version VARCHAR(32) CHARACTER SET ascii NOT NULL,
    definition_digest CHAR(71) CHARACTER SET ascii NOT NULL,
    display_name VARCHAR(320) NOT NULL,
    configuration JSON NOT NULL,
    not_ready_reasons JSON NOT NULL,
    position_x DOUBLE NOT NULL,
    position_y DOUBLE NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id)
) ENGINE=InnoDB;
