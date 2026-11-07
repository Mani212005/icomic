DROP TABLE account_policy;
DROP TABLE sessions;
DROP INDEX uq_users_email_lower;
ALTER TABLE users DROP CONSTRAINT users_registered_credentials;
-- Guests cannot satisfy the restored NOT NULL columns, and their projects cascade with them.
DELETE FROM users WHERE account_kind = 'guest' AND (email IS NULL OR password_hash IS NULL);
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE users ALTER COLUMN email SET NOT NULL;
ALTER TABLE users DROP COLUMN account_kind;
