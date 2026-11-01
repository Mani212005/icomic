-- Initial schema migration for Icomic based on SPEC.md Section 9

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS plans (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(64) NOT NULL,
    entitlements JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO plans (id, name, entitlements) VALUES
('free', 'Free Tier', '{"max_daily_images": 10, "max_projects": 3}'::jsonb),
('pro', 'Pro Creator', '{"max_daily_images": 200, "max_projects": 50}'::jsonb)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    date_of_birth DATE NOT NULL,
    country_code VARCHAR(3) NOT NULL,
    plan_id VARCHAR(64) REFERENCES plans(id) DEFAULT 'free',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS projects (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    original_script TEXT NOT NULL,
    chapter_count INT NOT NULL DEFAULT 1,
    art_style VARCHAR(64) NOT NULL DEFAULT 'classic-comic',
    status VARCHAR(64) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chapters (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    chapter_number INT NOT NULL,
    title VARCHAR(255) NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    status VARCHAR(64) NOT NULL DEFAULT 'pending'
);

CREATE TABLE IF NOT EXISTS generation_jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_type VARCHAR(64) NOT NULL,
    stage VARCHAR(64) NOT NULL,
    model_id VARCHAR(128) NOT NULL,
    status VARCHAR(64) NOT NULL DEFAULT 'pending',
    idempotency_key VARCHAR(255) UNIQUE NOT NULL,
    estimated_cost NUMERIC(10, 4) NOT NULL DEFAULT 0.0000,
    actual_cost NUMERIC(10, 4),
    hashed_user_id VARCHAR(64) NOT NULL,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS characters (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    reference_image_url TEXT,
    reference_image_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    visual_attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    approved_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS panels (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chapter_id UUID NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
    panel_number INT NOT NULL,
    scene_description TEXT NOT NULL DEFAULT '',
    dialogue TEXT NOT NULL DEFAULT '',
    narration TEXT NOT NULL DEFAULT '',
    image_prompt TEXT NOT NULL DEFAULT '',
    status VARCHAR(64) NOT NULL DEFAULT 'pending'
);

CREATE TABLE IF NOT EXISTS pages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chapter_id UUID NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
    page_number INT NOT NULL,
    panel_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    draft_image_url TEXT,
    draft_image_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    draft_approved_at TIMESTAMPTZ,
    line_art_image_url TEXT,
    line_art_image_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    line_art_approved_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS colour_palettes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    page_id UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    suggestion_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    palette JSONB NOT NULL DEFAULT '[]'::jsonb,
    user_modified_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS colour_regions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    page_id UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    region_label VARCHAR(128) NOT NULL,
    fill_colour_hex VARCHAR(16) NOT NULL,
    fill_applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fill_applied_by VARCHAR(32) NOT NULL DEFAULT 'user'
);

CREATE TABLE IF NOT EXISTS stage_approvals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    entity_type VARCHAR(64) NOT NULL,
    entity_id UUID NOT NULL,
    approved_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    approved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    credit_estimate_at_approval NUMERIC(10, 4) NOT NULL DEFAULT 0.0000
);

CREATE TABLE IF NOT EXISTS credit_ledger (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    generation_job_id UUID REFERENCES generation_jobs(id) ON DELETE CASCADE,
    transaction_type VARCHAR(32) NOT NULL,
    amount NUMERIC(10, 4) NOT NULL,
    balance_after NUMERIC(10, 4) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS exports (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    file_url TEXT NOT NULL,
    format VARCHAR(32) NOT NULL DEFAULT 'pdf',
    status VARCHAR(64) NOT NULL DEFAULT 'pending',
    provenance_metadata_preserved BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
