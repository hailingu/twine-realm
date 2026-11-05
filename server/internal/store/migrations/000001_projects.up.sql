CREATE TABLE projects (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    name VARCHAR(320) NOT NULL,
    revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;

CREATE TABLE repositories (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    path TEXT NOT NULL,
    path_hash BINARY(32) NOT NULL UNIQUE
) ENGINE=InnoDB;

CREATE TABLE services (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    project_id CHAR(36) CHARACTER SET ascii NOT NULL,
    repository_id CHAR(36) CHARACTER SET ascii NOT NULL,
    display_name VARCHAR(320) NOT NULL,
    head_commit VARCHAR(64) CHARACTER SET ascii NULL,
    position_x DOUBLE NOT NULL,
    position_y DOUBLE NOT NULL,
    UNIQUE KEY project_repository (project_id, repository_id),
    FOREIGN KEY (project_id) REFERENCES projects(id),
    FOREIGN KEY (repository_id) REFERENCES repositories(id)
) ENGINE=InnoDB;

CREATE TABLE mutation_receipts (
    id CHAR(36) CHARACTER SET ascii PRIMARY KEY,
    intent_hash BINARY(32) NOT NULL,
    response JSON NULL,
    created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;
