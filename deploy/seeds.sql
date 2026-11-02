-- Seeds for Icomic database
-- Seed plans as defined in docs/SPEC.md Section 9 and Section 11.5

INSERT INTO plans (id, name, entitlements) VALUES
('free', 'Free Tier', '{"max_daily_images": 10, "max_projects": 3, "allowed_formats": ["pdf"]}'::jsonb),
('pro', 'Pro Creator', '{"max_daily_images": 200, "max_projects": 50, "allowed_formats": ["pdf", "png_zip"]}'::jsonb),
('enterprise', 'Studio Enterprise', '{"max_daily_images": 1000, "max_projects": 500, "allowed_formats": ["pdf", "png_zip"]}'::jsonb)
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    entitlements = EXCLUDED.entitlements;

-- Seed default development user for local development and testing
INSERT INTO users (id, email, password_hash, plan_id, created_at, updated_at) VALUES
('00000000-0000-0000-0000-000000000001', 'dev@icomic.local', '$2a$10$wN9P31K9LpQn88bWq4KqreD/4aUa240GZc5R9lHjEaZ9a.Z7yN.2G', 'pro', NOW(), NOW())
ON CONFLICT (id) DO NOTHING;

-- Seed initial credit balance for default dev user (nullable project_id and generation_job_id)
INSERT INTO credit_ledger (id, user_id, project_id, generation_job_id, transaction_type, amount, balance_after, created_at)
SELECT '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', NULL, NULL, 'adjustment', 100.0, 100.0, NOW()
WHERE NOT EXISTS (SELECT 1 FROM credit_ledger WHERE user_id = '00000000-0000-0000-0000-000000000001');
