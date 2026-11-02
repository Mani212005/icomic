-- Seeds for Icomic database
-- Seed plans as defined in docs/SPEC.md Section 9 and Section 11.5

INSERT INTO plans (id, name, entitlements) VALUES
('free', 'Free Tier', '{"max_daily_images": 10, "max_projects": 3, "allowed_formats": ["pdf"]}'::jsonb),
('pro', 'Pro Creator', '{"max_daily_images": 200, "max_projects": 50, "allowed_formats": ["pdf", "png_zip"]}'::jsonb),
('enterprise', 'Studio Enterprise', '{"max_daily_images": 1000, "max_projects": 500, "allowed_formats": ["pdf", "png_zip"]}'::jsonb)
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    entitlements = EXCLUDED.entitlements;
