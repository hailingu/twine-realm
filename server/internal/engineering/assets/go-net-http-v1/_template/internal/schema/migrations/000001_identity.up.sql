CREATE TABLE service_identity (
    id TINYINT PRIMARY KEY,
    initialized_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;

INSERT INTO service_identity(id, initialized_at) VALUES(1, UTC_TIMESTAMP(6));

CREATE TABLE service_migration_digests (
    version BIGINT UNSIGNED PRIMARY KEY,
    digest CHAR(64) CHARACTER SET ascii NOT NULL
) ENGINE=InnoDB;
