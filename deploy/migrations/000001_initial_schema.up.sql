-- Initial schema migration for Icomic
-- Conforms strictly to docs/SPEC.md Section 9 and docs/ARCHITECTURE.md Section 5 & 7.

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Plans table: account tiers and entitlements for paywall extension (SPEC 9, 11.5)
CREATE TABLE IF NOT EXISTS plans (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(64) NOT NULL,
    entitlements JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed default plans (SPEC 9, 11.5)
INSERT INTO plans (id, name, entitlements) VALUES
('free', 'Free Tier', '{"max_daily_images": 10, "max_projects": 3}'::jsonb),
('pro', 'Pro Creator', '{"max_daily_images": 200, "max_projects": 50}'::jsonb)
ON CONFLICT (id) DO NOTHING;

-- Users table: creator accounts (SPEC 9, 11.5)
-- Owner decisions 2026-11-02:
-- 1. No geo-blocking or region restrictions (users are global): country/region columns removed.
-- 2. 18+ age gate is deferred (warning to be added later): date_of_birth is nullable with no enforced age logic.
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    date_of_birth DATE, -- Nullable: 18+ age gate deferred per owner decision 2026-11-02; no enforced age logic
    plan_id VARCHAR(64) REFERENCES plans(id) DEFAULT 'free',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_plan_id ON users(plan_id);

-- Projects table: comic book projects (SPEC 9)
CREATE TABLE IF NOT EXISTS projects (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    original_script TEXT NOT NULL,
    chapter_count INT NOT NULL DEFAULT 1 CHECK (chapter_count > 0),
    art_style VARCHAR(64) NOT NULL DEFAULT 'classic-comic',
    status VARCHAR(64) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'in_progress', 'completed', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_projects_user_id ON projects(user_id);
CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status);

-- Chapters table: chapter divisions of the project script (SPEC 9)
CREATE TABLE IF NOT EXISTS chapters (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    chapter_number INT NOT NULL CHECK (chapter_number > 0),
    title VARCHAR(255) NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    status VARCHAR(64) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'approved', 'completed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_chapters_project_chapter_number UNIQUE (project_id, chapter_number)
);

CREATE INDEX IF NOT EXISTS idx_chapters_project_id ON chapters(project_id);

-- Generation jobs table: asynchronous AI generation tasks (SPEC 9, ARCHITECTURE 5.2, contracts/schemas/job.schema.json)
-- Precedes characters and pages due to foreign key references.
CREATE TABLE IF NOT EXISTS generation_jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_type VARCHAR(64) NOT NULL CHECK (job_type IN (
        'script_analysis',
        'chapter_outline',
        'panel_text',
        'character_reference',
        'draft_sketch',
        'final_line_art',
        'colour_suggestion',
        'pdf_assembly'
    )),
    stage VARCHAR(64) NOT NULL CHECK (stage IN (
        'draft',
        'line_art',
        'colour',
        'reference',
        'text',
        'export'
    )),
    model_id VARCHAR(128) NOT NULL,
    status VARCHAR(64) NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending',
        'running',
        'done',
        'failed',
        'cancelled'
    )),
    idempotency_key VARCHAR(255) UNIQUE NOT NULL,
    estimated_cost NUMERIC(10, 4) NOT NULL DEFAULT 0.0000 CHECK (estimated_cost >= 0),
    actual_cost NUMERIC(10, 4) CHECK (actual_cost IS NULL OR actual_cost >= 0),
    hashed_user_id VARCHAR(64) NOT NULL,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_generation_jobs_project_id ON generation_jobs(project_id);
CREATE INDEX IF NOT EXISTS idx_generation_jobs_user_id ON generation_jobs(user_id);
CREATE INDEX IF NOT EXISTS idx_generation_jobs_status ON generation_jobs(status);
CREATE INDEX IF NOT EXISTS idx_generation_jobs_stage ON generation_jobs(stage);
CREATE INDEX IF NOT EXISTS idx_generation_jobs_project_stage_status ON generation_jobs(project_id, stage, status);
CREATE INDEX IF NOT EXISTS idx_generation_jobs_idempotency ON generation_jobs(idempotency_key);

-- Characters table: character definitions and approved reference images (SPEC 9, 3.2)
-- approved_at semantics: only counts if approved_at > latest job completed_at and approved_at > updated_at (stale-approval rule).
CREATE TABLE IF NOT EXISTS characters (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    reference_image_url TEXT,
    reference_image_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    visual_attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    approved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_characters_project_id ON characters(project_id);
CREATE INDEX IF NOT EXISTS idx_characters_ref_image_job_id ON characters(reference_image_job_id);
CREATE INDEX IF NOT EXISTS idx_characters_approved_at ON characters(approved_at);

-- Panels table: scene descriptions and dialogue per chapter (SPEC 9, 3.5)
CREATE TABLE IF NOT EXISTS panels (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chapter_id UUID NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
    panel_number INT NOT NULL CHECK (panel_number > 0),
    scene_description TEXT NOT NULL DEFAULT '',
    dialogue TEXT NOT NULL DEFAULT '',
    narration TEXT NOT NULL DEFAULT '',
    image_prompt TEXT NOT NULL DEFAULT '',
    status VARCHAR(64) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_panels_chapter_panel_number UNIQUE (chapter_id, panel_number)
);

CREATE INDEX IF NOT EXISTS idx_panels_chapter_id ON panels(chapter_id);

-- Pages table: panel allocations, draft sketches, and line art (SPEC 9, 3.5.1, 3.6, 3.7)
-- approved_at semantics: draft_approved_at and line_art_approved_at only count if newer than latest job completed_at and page updated_at.
CREATE TABLE IF NOT EXISTS pages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chapter_id UUID NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
    page_number INT NOT NULL CHECK (page_number > 0),
    panel_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    draft_image_url TEXT,
    draft_image_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    draft_approved_at TIMESTAMPTZ,
    line_art_image_url TEXT,
    line_art_image_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    line_art_approved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pages_chapter_page_number UNIQUE (chapter_id, page_number)
);

CREATE INDEX IF NOT EXISTS idx_pages_chapter_id ON pages(chapter_id);
CREATE INDEX IF NOT EXISTS idx_pages_draft_image_job_id ON pages(draft_image_job_id);
CREATE INDEX IF NOT EXISTS idx_pages_line_art_image_job_id ON pages(line_art_image_job_id);

-- Colour palettes table: AI-suggested or user-modified palettes per page (SPEC 9, 3.8.1)
CREATE TABLE IF NOT EXISTS colour_palettes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    page_id UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    suggestion_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    palette JSONB NOT NULL DEFAULT '[]'::jsonb,
    user_modified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_colour_palettes_page_id ON colour_palettes(page_id);
CREATE INDEX IF NOT EXISTS idx_colour_palettes_suggestion_job_id ON colour_palettes(suggestion_job_id);

-- Colour regions table: deterministic client-side fills applied to page line art (SPEC 9, 3.8.3)
CREATE TABLE IF NOT EXISTS colour_regions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    page_id UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    region_label VARCHAR(128) NOT NULL,
    fill_colour_hex VARCHAR(16) NOT NULL,
    fill_applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fill_applied_by VARCHAR(32) NOT NULL DEFAULT 'user' CHECK (fill_applied_by IN ('user', 'ai_auto'))
);

CREATE INDEX IF NOT EXISTS idx_colour_regions_page_id ON colour_regions(page_id);

-- Stage approvals table: explicit user sign-offs gating pipeline stages (SPEC 9, ARCHITECTURE 5.3)
CREATE TABLE IF NOT EXISTS stage_approvals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    entity_type VARCHAR(64) NOT NULL CHECK (entity_type IN (
        'chapter_outline',
        'character_reference',
        'draft_page',
        'line_art_page',
        'colour_page'
    )),
    entity_id UUID NOT NULL,
    approved_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    approved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    credit_estimate_at_approval NUMERIC(10, 4) NOT NULL DEFAULT 0.0000 CHECK (credit_estimate_at_approval >= 0)
);

CREATE INDEX IF NOT EXISTS idx_stage_approvals_project_id ON stage_approvals(project_id);
CREATE INDEX IF NOT EXISTS idx_stage_approvals_entity ON stage_approvals(entity_type, entity_id, approved_at DESC);
CREATE INDEX IF NOT EXISTS idx_stage_approvals_approved_by ON stage_approvals(approved_by_user_id);

-- Credit ledger table: transactional audit log of credit debits, refunds, and adjustments (SPEC 9, ARCHITECTURE 5.1, contracts/schemas/credit.schema.json)
CREATE TABLE IF NOT EXISTS credit_ledger (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    generation_job_id UUID REFERENCES generation_jobs(id) ON DELETE CASCADE,
    transaction_type VARCHAR(32) NOT NULL CHECK (transaction_type IN ('debit', 'refund', 'adjustment')),
    amount NUMERIC(10, 4) NOT NULL CHECK (amount >= 0),
    balance_after NUMERIC(10, 4) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_credit_ledger_user_id ON credit_ledger(user_id);
CREATE INDEX IF NOT EXISTS idx_credit_ledger_project_id ON credit_ledger(project_id);
CREATE INDEX IF NOT EXISTS idx_credit_ledger_job_id ON credit_ledger(generation_job_id);
CREATE INDEX IF NOT EXISTS idx_credit_ledger_created_at ON credit_ledger(created_at DESC);

-- Exports table: assembled comic book outputs (SPEC 9, 3.10, 11.4)
-- Provenance metadata preserved flag must be true before marking export complete.
CREATE TABLE IF NOT EXISTS exports (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    file_url TEXT,
    format VARCHAR(32) NOT NULL DEFAULT 'pdf' CHECK (format IN ('pdf', 'png_zip')),
    status VARCHAR(64) NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending',
        'running',
        'done',
        'failed',
        'cancelled'
    )),
    provenance_metadata_preserved BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT chk_exports_provenance_preserved CHECK (status != 'done' OR provenance_metadata_preserved = TRUE)
);

CREATE INDEX IF NOT EXISTS idx_exports_project_id ON exports(project_id);
CREATE INDEX IF NOT EXISTS idx_exports_status ON exports(status);
