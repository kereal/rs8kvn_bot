-- Migration: 039_create_audit_log (down)
-- Description: Откат создания журнала аудита.

DROP INDEX IF EXISTS idx_audit_log_event;
DROP INDEX IF EXISTS idx_audit_log_created_at;
DROP INDEX IF EXISTS idx_audit_log_entity;
DROP TABLE IF EXISTS audit_log;
