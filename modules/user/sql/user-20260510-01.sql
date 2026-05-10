-- +migrate Up
-- no-op migration — YUJ-399 worker was deferred; original idx_user_online_last_online
-- was meant to accelerate the worker's DISTINCT uid scan, which is no longer needed.
-- File retained as placeholder to keep migration ledger consistent for any env that
-- already registered this version.
SELECT 1;

-- +migrate Down
SELECT 1;
