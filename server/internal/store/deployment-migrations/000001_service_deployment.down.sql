-- 先移除本模块从属记录，不触碰任何项目业务数据或Docker资源。
DROP TABLE deployment_events;
DROP TABLE deployment_receipts;
DROP TABLE deployment_operations;
DROP TABLE deployment_targets;
DROP TABLE deployment_instances;
