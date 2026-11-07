-- Accounts: guest versus registered identities, server-side sessions, project limits.
-- Existing users become guests, except the seeded dev identity used by local and e2e runs.
ALTER TABLE users ADD COLUMN account_kind VARCHAR(16) NOT NULL DEFAULT 'guest'
    CHECK (account_kind IN ('guest', 'registered'));
UPDATE users SET account_kind = 'registered' WHERE id = '00000000-0000-0000-0000-000000000001';

-- A guest has no email or password. A registered user has both.
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_registered_credentials
    CHECK (account_kind = 'guest' OR (email IS NOT NULL AND password_hash IS NOT NULL));
CREATE UNIQUE INDEX uq_users_email_lower ON users (LOWER(email)) WHERE email IS NOT NULL;

-- Sessions store only a SHA-256 of the cookie token, so a database read cannot replay a session.
CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    token_hash BYTEA NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);

-- Runtime policy is durable and operators can change it without a deployment.
-- registered_max_projects is a ceiling; guests are bound to exactly their single project.
CREATE TABLE account_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    guest_max_projects INT NOT NULL DEFAULT 1 CHECK (guest_max_projects >= 0),
    registered_max_projects INT NOT NULL DEFAULT 100 CHECK (registered_max_projects >= 1)
);
INSERT INTO account_policy (singleton) VALUES (TRUE);
