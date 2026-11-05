CREATE TABLE service_contract_versions (
    service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    contract_id VARCHAR(80) CHARACTER SET ascii NOT NULL,
    version VARCHAR(32) CHARACTER SET ascii NOT NULL,
    digest CHAR(71) CHARACTER SET ascii NOT NULL,
    payload JSON NOT NULL,
    PRIMARY KEY(service_id, contract_id, version),
    FOREIGN KEY(service_id) REFERENCES services(id),
    FOREIGN KEY(project_id) REFERENCES projects(id)
) ENGINE=InnoDB;

CREATE TABLE service_contracts (
    service_id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    contract_id VARCHAR(80) CHARACTER SET ascii NOT NULL,
    version VARCHAR(32) CHARACTER SET ascii NOT NULL,
    FOREIGN KEY(service_id, contract_id, version) REFERENCES service_contract_versions(service_id, contract_id, version),
    FOREIGN KEY(project_id) REFERENCES projects(id)
) ENGINE=InnoDB;

CREATE TABLE database_bindings (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    source_service_id CHAR(36) CHARACTER SET ascii NOT NULL,
    source_port VARCHAR(80) CHARACTER SET ascii NOT NULL,
    target_middleware_id CHAR(36) CHARACTER SET ascii NOT NULL,
    target_port VARCHAR(80) CHARACTER SET ascii NOT NULL,
    source_definition JSON NOT NULL,
    target_definition JSON NOT NULL,
    protocol VARCHAR(40) CHARACTER SET ascii NOT NULL,
    contract_id VARCHAR(120) CHARACTER SET ascii NOT NULL,
    database_id CHAR(36) CHARACTER SET ascii NOT NULL UNIQUE,
    database_name VARCHAR(48) CHARACTER SET ascii NOT NULL,
    purpose VARCHAR(16) CHARACTER SET ascii NOT NULL,
    credential_id CHAR(36) CHARACTER SET ascii NOT NULL,
    credential_version VARCHAR(20) CHARACTER SET ascii NOT NULL,
    deleted_at DATETIME(6) NULL,
    FOREIGN KEY(project_id) REFERENCES projects(id),
    FOREIGN KEY(source_service_id) REFERENCES services(id),
    FOREIGN KEY(target_middleware_id) REFERENCES middlewares(id)
) ENGINE=InnoDB;
