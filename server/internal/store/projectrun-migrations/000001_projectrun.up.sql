-- 项目父操作独立拥有执行账本，原组件历史保持不变。
CREATE TABLE projectrun_instances (
 project_id VARCHAR(64) PRIMARY KEY,
 generation BIGINT UNSIGNED NOT NULL,
 observation_epoch BIGINT UNSIGNED NOT NULL DEFAULT 1,
 current_operation_id VARCHAR(64) NOT NULL,
 desired VARCHAR(16) NOT NULL,
 state VARCHAR(32) NOT NULL,
 facts JSON NULL
);
CREATE TABLE projectrun_operations (
 id VARCHAR(64) PRIMARY KEY,
 project_id VARCHAR(64) NOT NULL,
 generation BIGINT UNSIGNED NOT NULL,
 execution BIGINT UNSIGNED NOT NULL DEFAULT 0,
 lease_until DATETIME(6) NULL,
 payload JSON NOT NULL,
 target_json JSON NOT NULL,
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY projectrun_generation(project_id,generation)
);
CREATE TABLE projectrun_receipts (
 id VARCHAR(64) PRIMARY KEY,
 intent_hash BINARY(32) NOT NULL,
 response JSON NULL,
 created_at DATETIME(6) NOT NULL
);
CREATE TABLE projectrun_events (
 sequence BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
 project_id VARCHAR(64) NOT NULL,
 operation_id VARCHAR(64) NOT NULL,
 payload JSON NOT NULL,
 created_at DATETIME(6) NOT NULL
);
