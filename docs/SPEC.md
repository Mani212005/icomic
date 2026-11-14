# Specs : Icomic
<!-- version: 2 -->

## 1. Project Overview

project name - Icomic

### 1.1 Purpose

Build an AI-powered comic book generator designed to be sold to end users (free tier first, then a paywall).

The user provides a written script.

The system converts the script into a comic book through a staged, approval-gated pipeline.

The user can:

- Enter a script.
- Select the number of chapters.
- Select an art style.
- Define and approve character profiles and reference images.
- Review a cheap draft sketch of each page and approve composition.
- Review and approve the finished page (painted by default; ink is an explicit option).
- Colour ink pages (AI-suggested palette with automatic apply, fully editable, paint-bucket fill tool).
- Download the finished comic book.
- Publish the comic book.

The system must use Meta AI models only for all AI tasks.

### 1.2 Product Goal

Allow a user to turn a written story into a publishable comic book without drawing skills.

The system must provide control over the story, images, and generation cost at every stage.

The product must be ready to introduce paid plans and access entitlements without rewriting the billing layer.

### 1.3 Target Users

- Writers
- Comic book creators
- Independent authors
- Artists
- Content creators
- Students

User age warning (18+ or 21+, to be confirmed with owner) is deferred to a later release; no age gate is enforced in the initial build (see section 11.2).

### 1.4 AI Model Policy

All AI features use Meta models only (Meta Model API at `api.meta.ai`).

Image generation uses `muse-image-1.0`, which is currently available only as a hosted API; no open-weights image model from Meta exists. Per the owner decision of 2026-11-02, generated images are Meta-hosted: the real image adapter returns Meta-hosted URLs or Meta-delivered bytes per Meta's API, and the image hosting seam (ImageHostingSeam in Go, ImageHostingPolicy in Python) stays configurable. Our own storage still holds approved working copies and export PDFs as the architecture already says. The image generation adapter port must remain independent so a future self-hosted or alternative image model can be substituted without rewriting the product.

Meta API keys come only after the whole project is built on fakes, with the Meta adapters inert behind the ports until then. The product must run end-to-end with no network access and no API key in development and test, using deterministic fake adapters (see section 8 and ARCHITECTURE.md).

---

## Thought Process

When a person tries to write a comic, what he basically does is, he first defines the characters and how the main character would look like, how the other characters would look like. Second, then he has a script, right, where he wants to portray how the stuff would move.

So first a script, then his character visualization, what every character is, he gives them a character definition like a name, what he thinks their age might be and how they will look, right, personality. And then he converts using those characters into a story that he depicts using art.

Now in that art, he makes sure that the characters stay stable and they don't change too much because characters are same, right. And another thing is, he does not draw the first, he does not draw the full draft at the first time. He draws a rough sketch, which is, you could call it temporary sketch of how it would look like.

And then he tries to visualize would it look good. And once he has finalized on how a temporary sketch, then he finalizes the version of it. The temporary sketch is generally less effort. And you could say they are stick designs or start of how the character or the comic should look like.

Then we move to the final design. Once he has done the final design, then the coloring part comes.

Then he colors them, deciding on what color does the background should look like. So the comic should have like, should be so the comic should be divided into three layers, right. The first is, first is not three exactly, I'll state all the layers, then I'll tell you how many layers there are. The comic should be the first is the script that the user provides.

The second is the character design that based on the script the user has provided. The comic should, you should make the LLM should make the character design of them. The comic should or the person should know

(The captain's thought-process note ends here, cut off mid-sentence in the pasted spec. Its intended flow: script, then character design, then rough sketch (temporary, low effort), then final line art, then coloring and backgrounds, with characters kept stable throughout.)

---

## 2. Core User Flow

The main pipeline is:

1. User creates a project.
2. User provides a script.
3. System extracts characters; user reviews and approves character profiles and reference images. (Approval gate.)
4. System asks how many chapters the user wants.
5. User selects the number of chapters and art style.
6. System analyzes the script and creates a chapter outline.
7. User reviews and approves the outline. (Approval gate.)
8. For each chapter, the system generates panel text (scene descriptions, dialogue, narration). The user edits panel text freely. No approval gate - panel text feeds the draft step.
9. System automatically partitions panels into pages (using a configurable maximum panels per page). The user can move panels between pages before draft generation.
10. System generates one rough, stick-style draft image per page. User reviews draft composition and approves or regenerates. (Approval gate - no final image is generated before draft approval.)
11. System generates a finished painted page per page by default, with ink available as an explicit option. User reviews and approves the selected finish.
12. Ink pages use the optional colour stage: the system suggests a palette, the user edits or overrides it, applies paint-bucket fills, and approves the result. Painted pages are already finished and skip this stage.
13. System assembles the comic book.
14. User reviews the final comic and downloads or publishes.

The system must not generate draft sketches before character reference images and chapter outline are approved.
The ink option requires an approved draft. The direct-from-brief painted default does not.

---

## 3. Functional Requirements

### 3.1 Script Input

The user must be able to provide a script.

Supported input:

- Plain text
- Markdown
- TXT file
- PDF file

The system must:

- Validate the script.
- Reject empty input.
- Display the script word count.
- Allow the user to edit the script.
- Save the script to the project. The editor saves the Script stage's title, style, chapter count and script on its own after a pause in typing (about one second), and shows Unsaved changes, Saving, Saved, or Not saved with the reason and a Retry. Character, outline and panel edit forms autosave the same way; their Save button saves at once and closes the form. Saves are sent one at a time in edit order, so a slow earlier save cannot overwrite a newer edit and its answer never marks newer edits as saved. Leaving the page with unsaved or failed edits asks for confirmation.

The system must preserve the original script.

The system must not overwrite the original script during story generation.

### 3.2 Character Design

The system must identify the main characters from the script.

Each character must have a character profile.

The profile must contain:

- Character name
- Character description
- Age range
- Appearance
- Hair style
- Clothing
- Personality
- Important visual features

The system must generate a character reference image for each main character using the image adapter. Automatic analysis selects at most six main characters by source-name mention frequency, with extraction order breaking ties. Other extracted profiles remain as extras in the story and require profile approval; their reference images are optional until explicitly needed. No extracted profile is discarded. See `docs/GENERATION_POLICY.md` for the default policy and its limitations.

The user must be able to approve or regenerate the reference image.

Stale approvals after regeneration or edit: an approval only counts if its `approved_at` is newer than the entity's latest completed generation job or edit, so regenerating or editing automatically makes the approval stale and blocks downstream generation until it is re-approved.

Approved character references must be reused during all subsequent image generation stages.

The system must attempt to preserve character identity across all chapters (via reference image anchoring as the API supports).

The system must wait for user approval of character profiles and reference images before proceeding to chapter selection and outline generation.

### 3.3 Chapter Selection

The system must ask:

"How many chapters do you want for your comic book?"

The user can select the number of chapters.

The system must:

- Divide the script into the selected number of chapters.
- Preserve the main story.
- Preserve the main characters.
- Preserve important events.
- Avoid unnecessary story changes.

The system must warn the user if the selected chapter count is not suitable for the script length.

### 3.4 Story Outline

The system must generate a story outline referencing the approved characters.

Each chapter must contain:

- Chapter number
- Chapter title
- Chapter summary
- Important events
- Characters
- Ending or cliffhanger

The user must be able to edit the outline.

The system must wait for user approval of the outline before proceeding to panel text generation.

### 3.5 Panel Text Generation

The system must convert each chapter into panel descriptions.

Each panel must contain:

- Panel number
- Scene description
- Character actions
- Camera angle
- Background
- Dialogue
- Narration
- Image generation prompt (derived text, editable)

The user must be able to edit:

- Panel descriptions
- Dialogue
- Narration
- Character actions
- Image prompts

Panel text is an editable intermediate; it is not an approval gate. Edited panel text feeds the draft image step directly.

Stale approvals after regeneration or edit: an approval only counts if its `approved_at` is newer than the entity's latest completed generation job or edit, so regenerating or editing automatically makes the approval stale and blocks downstream generation until it is re-approved.

### 3.5.1 Pagination and Page Layout

The Go API automatically builds pages from the panel list using a configurable maximum panels per page (a configuration value).

Each page record is populated with an ordered list of `panel_ids` for that page.

Before draft generation, the user can review page allocations and move panels between pages.

The system provides endpoints to inspect and adjust page allocations:
- The user can retrieve chapter pages and their assigned panels (`GET /api/v1/chapters/:id/pages`).
- The user can move panels between pages or reorder panels before initiating draft generation (`PATCH /api/v1/pages/:id`).
  The request carries the page's complete ordered `panel_ids`; naming a panel that sits on another page moves it. Every panel of a chapter stays on exactly one page, unknown, foreign or repeated IDs, over-full pages and emptied pages are rejected, and the move is applied atomically. Changing a page's allocation stales its draft, line art and painted approvals.
- The user can add and delete panels and move one panel at a time, all scoped to the chapter. Each is one atomic edit that stales the draft, line art and painted approvals of every page it changes, and numbers pages and panels again in reading order, so panel numbers always run 1 to N page by page.
  - `POST /api/v1/chapters/:id/panels/add` stores a new panel with a required `scene_description` at `position` on `page_id`, or alone on a new last page when `page_id` is left out. A page holds at most the configured maximum.
  - `POST /api/v1/chapters/:id/panels/move` places `panel_id` at `position` on `page_id`, which reorders it within its page or moves it to another. It follows the same rules as `PATCH /api/v1/pages/:id`.
  - `DELETE /api/v1/chapters/:id/panels/:panel_id` removes the panel. A page whose last panel goes is removed with it, unless that page holds generated art: the request is then refused with HTTP 409 and nothing is deleted, because it would delete paid work.
  - The editor asks for confirmation before deleting a panel.

### 3.6 Draft Sketch Stage

The system must generate one rough draft image per page (not one per panel).

One draft image is one call to the image adapter; it shows all panels for that page in a rough, stick-style composition.

The draft image lets the user review layout and composition before the next stage. The default direct-from-brief painted finish does not condition on that draft.

The user must be able to:

- Approve the draft (enables ink or draft-conditioned painting).
- Regenerate the draft (costs one additional image call).

Ink generation and draft-conditioned painting require an approved draft.

Stale approvals after regeneration or edit: an approval only counts if its `approved_at` is newer than the entity's latest completed generation job or edit, so regenerating or editing automatically makes the approval stale and blocks downstream generation until it is re-approved.

The draft stage costs one image call per attempt per page. Credit cost for the draft is displayed before generation.

### 3.7 Finished Page Stage

After the draft stage, the system generates and approves one finished painted page per page by default. The default variant is direct-from-brief. Draft-conditioned painting remains available, and `variant=ink` explicitly selects the existing black-and-white ink route. The default finish replaces the former line-art generation call and approval, so each page still uses one finish image call.

The user can choose Painted or Ink before generation, review the result, approve it, or regenerate it for one additional image call. Ink generation requires an approved draft; direct-from-brief painting requires approved character references and chapter outline but does not require a draft approval.

An approval only counts when it is newer than the latest page edit or generation. Regenerating or editing makes the approval stale and requires re-approval.

Exports use the latest valid approved painted page and fall back to the valid approved ink page for pages finished with ink. Existing line-art images and approvals remain available without migration.

The colour editor is available for approved ink pages. Painted pages are already finished and skip the ink colour-fill step.

### 3.8 Colouring

Colouring is a deterministic fill step applied on top of the approved line art. No image API call is made during colouring fill.

#### 3.8.1 AI Colour Palette Suggestion

Before the user starts colouring, the system must request a colour palette suggestion from a Meta language or vision model.

The suggestion must include:

- A palette name or theme.
- Per-region colour recommendations (background, character skin, hair, clothing, environment).
- A hex or RGB value for each recommended colour.

The system must apply the suggested palette automatically as the initial colour state.

The user can accept the suggested palette or override any colour.

#### 3.8.2 Manual Colour Editing

The user must be able to:

- See the full palette in use.
- Change any colour in the palette.
- Use an eyedropper to sample a colour from the canvas.
- Apply a colour to a region with a paint-bucket fill tool.

#### 3.8.3 Paint-Bucket Fill Tool

The paint-bucket fill tool must operate on the approved line art.

Fill regions are bounded by dark pixels in the line art layer.

The fill algorithm must:

- Use a flood-fill algorithm with a configurable darkness threshold so near-black anti-aliased pixels are treated as boundaries.
- Apply morphological gap-closing before fill so small gaps in the line art do not cause colour leakage.
- Provide a manual gap-closer tool so the user can draw small bridge strokes to close visible gaps before applying fill.

The fill is computed entirely in the browser (client-side) using ordinary code. No AI call and no API credit is consumed during fill.

#### 3.8.4 Canvas Undo

Client-side canvas undo in the colour editor must operate on page canvas stroke and fill history.

The user must be able to undo the last N fill actions on the page canvas (N is configurable, default 20).

#### 3.8.5 Colouring Approval

The user must approve the coloured result for each page before the page is marked complete.

### 3.9 Comic Book Editor

The system must provide a comic book editor.

The editor must allow the user to:

- View all chapters.
- View all panels.
- Regenerate a draft image.
- Regenerate a final line art image.
- Edit dialogue.
- Edit narration.
- Change panel order.
- Add a panel.
- Delete a panel.
- Preview the comic book.

The editor must save changes automatically.

The editor must display generation status.

### 3.10 Comic Book Export

The system must generate a downloadable comic book.

Supported output:

- PDF
- PNG images for individual pages

The PDF must contain:

- Cover page
- Chapter titles
- Comic panels
- Dialogue
- Narration
- Page numbers
- AI-generation disclosure (required; see section 11.4)
- Meta provenance metadata preserved (required; see section 11.4)
- Copyright or attribution information

The system must provide a preview before download.

The system must preserve image quality during export.

The system must allow the user to select a page size.

Initial page sizes:

- A4
- US Letter
- Custom comic page

---

## 4. AI Model Requirements

### 4.1 Model Restriction

All AI models used by the application must be Meta models.

The system must not use:

- OpenAI models
- Google Gemini models
- Anthropic models
- Midjourney
- Stable Diffusion models from other providers
- Other non-Meta AI models

Non-AI infrastructure is allowed.

Examples:

- PostgreSQL
- Redis
- Object storage
- Docker
- Go
- Python
- Svelte

### 4.2 Model Responsibilities

The model layer must support the following tasks:

| Task                   | Model requirement          |
| ---------------------- | -------------------------- |
| Script analysis        | Meta language model        |
| Chapter planning       | Meta language model        |
| Panel text generation  | Meta language model        |
| Dialogue generation    | Meta language model        |
| Character descriptions | Meta language model        |
| Colour palette suggestion | Meta language or vision model |
| Image generation (draft, painted finish or explicit ink, reference) | Meta image model (`muse-image-1.0`) |
| Image review           | Meta multimodal model      |
| Video generation       | Meta video model (deferred) |

The system must use the best available Meta model for each task.

The exact model must be configurable.

### 4.3 Model Access

The system must support two model access methods:

1. Meta-hosted API (`api.meta.ai`)
2. Self-hosted Meta models (open-weights, where available)

**Image generation note:** No open-weights image model from Meta exists. `muse-image-1.0` is hosted-only. Per the owner decision of 2026-11-02, generated images are Meta-hosted: the real image adapter returns Meta-hosted URLs or Meta-delivered bytes per Meta's API, and the image hosting seam (ImageHostingSeam in Go, ImageHostingPolicy in Python) stays configurable. Our own storage still holds approved working copies and export PDFs as the architecture already says. The image adapter port must stay independent so a future alternative image model can be substituted without rewriting the product.

The system must verify that the selected model supports the required task.

The system must not assume that a model available in the Meta AI application is available through an API.

The model adapter must support:

- Model selection
- API authentication
- Request creation
- Response parsing
- Error handling
- Retry handling
- Usage tracking
- Hashed user ID on every image call (see section 11.3)

### 4.4 Model Fallback

The system may use another Meta model if the primary model fails.

The fallback model must support the same task.

The system must not silently switch to a non-Meta model.

### 4.5 Prompt Pre-Moderation

All prompts sent to AI models must be pre-screened by the backend before dispatch.

A blocked prompt must return a plain error message to the user without calling the AI API.

The system must tolerate HTTP 400 content-policy responses from the API gracefully; on a content-policy 400 the user must see a plain, non-technical message and be given the option to revise the prompt.

### 4.6 AI-Generation Disclosure

All AI-generated content must be disclosed in the export and in the editor UI.

Meta's provenance watermarks and metadata embedded in generated images must be preserved through the export pipeline without alteration or removal.

---

## 5. Credit and Cost Management

### 5.1 Problem

Image generation consumes credits.

Large scripts can contain many chapters and panels.

Generating all images without user approval can waste credits.

The system must control generation cost at every stage.

### 5.2 Per-Stage Credit Estimates

Before each image generation stage, the system must display an estimate for that stage only.

**Pricing basis (verified):** Meta's `muse-image-1.0` charges a flat **$0.01 per image** regardless of size, resolution, quality, or reasoning strength. Colour palette suggestions use a language or vision model call (token-based pricing). Paint-bucket fill is ordinary code and costs no credits.

| Stage | Cost basis |
| ----- | ---------- |
| Draft sketch | $0.01 per page image attempt |
| Finished painted page or explicit ink | $0.01 per page image attempt |
| Character reference image | $0.01 per image attempt |
| Colour palette suggestion | Token cost of one language/vision model call (estimated from configured token price) |
| Paint-bucket fill | $0.00 (no API call) |

The system must calculate estimates using the actual configured per-image price. The system must not invent credit prices. If a price is not yet configured, the system must label the estimate as "estimate" and make it clear the price is unverified.

The estimate must contain:

- Stage name
- Number of images to be generated
- Estimated credits or cost
- Any assumptions made

### 5.3 Credit Approval

The system must ask the user to approve the estimated cost before each image generation stage.

The user must be able to:

- Approve generation for that stage
- Cancel generation while it is still pending (see 8.5): a job that has not reached the provider is stopped for good and its debit refunded; a job already with the provider finishes and is charged
- Reduce scope (e.g., generate one chapter at a time)

The system must not charge for an image that it has not attempted to generate.

### 5.4 Credit Ledger

The system must maintain a credit ledger.

Each generation operation must record:

- User ID
- Project ID
- Generation job ID
- Model ID
- Task type (draft_sketch | final_line_art | character_reference | colour_suggestion)
- Stage (draft | line_art | colour | reference)
- Number of images
- Input token usage (for language calls)
- Output token usage (for language calls)
- Estimated cost
- Actual cost
- Status
- Timestamp

The ledger must support:

- Credit deduction
- Credit refunds for eligible failures
- Usage history
- Cost reporting

Credit calculations must happen on the backend. The system must not rely only on frontend credit calculations.

### 5.5 Per-User Quotas and Global Kill Switch

The API has no hard spend cap. The system must therefore enforce spend limits in its own credit layer.

The system must support:

- A configurable lifetime per-book image budget (default 40), rolling per-user daily image cap (80) and monthly image cap (800). A 12-page book needs roughly 30 images, leaving room for revisions. Full refunds release image usage; new regenerations count, same-key replays do not.
- A global budget kill switch (the global stop switch): a single operator-set flag that halts all new paid generation (image, colour and text jobs that call the provider) for all users when the platform-wide spend reaches a configured threshold. It does not stop work that spends nothing at the provider: finished pages, downloads and PDF/PNG export keep working. Jobs committed but not yet sent are held, not sent, and can be cancelled for a refund.
- Operators must be able to update the kill-switch threshold and reset it without a code deployment.

When a quota or kill switch is active, the system must inform the user clearly (a structured `generation_stopped` error for the kill switch) and must not initiate the generation job.

### 5.6 Rate Limiting

The Meta Model API enforces a limit of 150 image requests per minute per team account (verified). The system must not exceed this.

The job queue concurrency setting for image generation jobs must be configured to keep the throughput at or below 150 requests per minute. The default concurrency setting must be conservative (recommend: 2 concurrent image workers; adjustable by operator).

### 5.7 Generation Limits

The system must support configurable limits.

Examples:

- Maximum script length
- Maximum chapters
- Maximum panels per chapter
- Maximum image generations per project
- Maximum concurrent generation jobs

The system must enforce these limits on the backend.

---

## 6. User Verification and Publishing

### 6.1 Verification Goal

The system must allow users to verify the project before they spend credits.

The system must also allow users to verify the final comic book before they publish it.

Verification must be a review step.

Verification must not mean that the user has purchased or consumed generation credits.

### 6.2 Pre-Draft Verification

Before draft generation, the user must be able to review:

- Chapter outline
- Character profiles and approved reference images
- Panel text (scenes, dialogue, narration)
- Number of draft images to generate
- Estimated draft cost

The system must require approval before generating drafts.

### 6.3 Pre-Line-Art Verification

Before finished page generation, the user must be able to review:

- Approved draft images
- Revised panel text (if edited after draft approval)
- Number of page finish images to generate
- Estimated finish cost

The system must require approval before generating page finish images.

Stale approvals after regeneration or edit: an approval only counts if its `approved_at` is newer than the entity's latest completed generation job or edit, so regenerating or editing automatically makes the approval stale and blocks downstream generation until it is re-approved.

### 6.4 Image Verification

After each image generation stage the system must allow the user to review each generated image.

The user must be able to:

- Approve the image
- Reject the image
- Regenerate the image
- Edit the image prompt and regenerate

Regeneration must display its additional credit cost before the call is made.

### 6.5 Final Verification

Before publishing, the system must show a final review screen.

The review must include:

- Complete comic book preview
- Missing images
- Missing dialogue
- Incorrect panel order
- Image quality warnings
- Export format
- Download size

The user must approve the final comic book before publishing.

### 6.6 Publishing

The initial version must support downloading the comic book.

The user can publish the downloaded PDF on external platforms.

The system must not claim that a comic book is commercially publishable without checking the relevant model license and publishing platform requirements.

The system must provide:

- AI-generated content disclosure
- Meta provenance metadata (preserved, not stripped)
- Model and asset information
- Copyright information
- License information, when available

The system must not claim that the user owns copyright in all AI-generated content.

The system must show the applicable terms before commercial publishing.

---

## 7. Optional Video Generation

**Status: Deferred.** Video generation is not part of the first release or the current roadmap.

Meta does not currently offer any video generation model through the `api.meta.ai` commercial API. No open-weights Meta video model exists. This feature cannot be built under the Meta-only requirement until a suitable Meta video API becomes available.

The system may add video generation later when a Meta-hosted or open-weights video model is available.

If implemented, the feature must allow the user to:

- Select a comic panel.
- Generate a short animated scene.
- Preview the animation.
- Download the animation.

The system must estimate video generation costs before generation.

The system must require user approval.

The system must use a Meta video model with confirmed commercial API access.

---

## 8. Technical Architecture

### 8.1 Required Tech Stack

| Component      | Technology            |
| -------------- | --------------------- |
| Frontend       | Svelte                |
| Backend API    | Go                    |
| AI service     | Python + FastAPI      |
| Database       | PostgreSQL            |
| Job queue      | Redis                 |
| Object storage | S3-compatible storage |
| Deployment     | Docker                |
| AI models      | Meta models only      |

### 8.2 Architecture

The system must separate the application into services.

```text
                 Svelte Frontend
                        |
                        v
                  Go API Server
                        |
          +-------------+-------------+
          |             |             |
          v             v             v
      PostgreSQL      Redis       Object Storage
          |             |
          |             v
          |        Python AI Worker
          |             |
          |      (Ports and Adapters)
          |        /    |    \
          |   Language Vision Image
          |   Adapter  Adapter Adapter
          |       (fake or real Meta)
          |             |
          +-------------+
                        |
                        v
                 Generated Images
                        |
                        v
               Colour Editor (Svelte)
                        |
                        v
                    PDF Export
```

See ARCHITECTURE.md for the full ports-and-adapters design and fake adapter strategy.

### 8.3 Go Backend

The Go backend must handle:

- User authentication and session management
- Project management
- Script uploads
- Chapter management
- Credit management and ledger writes
- Credit approval gates (no image job created without approval)
- Generation job creation and status
- Download links
- Publishing metadata
- Per-user quota enforcement
- Global kill-switch check before creating any image generation job
- Hashed user ID generation for image calls
- Prompt pre-moderation dispatch to Python service

The Go backend must not run long image generation requests directly.

It must create jobs and return a job ID.

All credit-ledger writes must be transactional.

No image generation job must be created unless the user has explicitly approved the stage and its cost.

### 8.4 Python AI Service

The Python service must handle:

- Script analysis
- Story generation
- Chapter generation
- Panel text generation
- Character profile generation
- Colour palette suggestion
- Image generation via the image adapter port
- Image review via the vision adapter port
- Prompt pre-moderation (language model call before any AI dispatch)
- PDF assembly

The service must expose internal APIs versioned under a common OpenAPI/JSON Schema contract shared with the Go backend (see ARCHITECTURE.md).

The service must not expose model API keys to the frontend.

All AI calls must go through adapter ports so fakes can be substituted for testing.

### 8.5 Background Jobs

Image generation must use background jobs via the Redis queue.

The system must support:

- Job creation
- Job execution
- Job retries (idempotent; re-executing a job must not double-charge credits)
- Job cancellation, pending-only and honest: cancelling a job that has not reached the provider atomically makes it terminal `cancelled`, ensures the dispatcher never sends it, and refunds its debit in the same transaction (idempotent on repeats). A job already claimed or sent is reported `settling`: it is not stopped, keeps its result and cost, and is charged. A terminal status is never reversed, so a late completion cannot overwrite a cancellation. Provider-side cancellation is not attempted.
- Job status
- Failed job recovery

The system must not lose a job when a worker restarts.

Each job must be idempotent: re-running a completed job must return its existing result without generating a new image or charging credits again.

---

## 9. Database Design

The initial database must contain the following tables.

### users

- id (UUID)
- email
- password_hash
- plan_id (FK to plans; extension point for paywall)
- created_at
- updated_at

### plans

- id
- name (free | paid | ...)
- entitlements (JSONB; extension point - stores per-plan limits and feature flags)
- created_at

### projects

- id (UUID)
- user_id (FK)
- title
- original_script
- chapter_count
- art_style
- status
- created_at
- updated_at

### chapters

- id (UUID)
- project_id (FK)
- chapter_number
- title
- summary
- important_events (JSONB array of text)
- character_names (JSONB array of text)
- ending
- status

### characters

- id (UUID)
- project_id (FK)
- name
- description
- reference_image_url
- reference_image_job_id (FK to generation_jobs)
- visual_attributes (JSONB)
- approved_at

### panels

- id (UUID)
- chapter_id (FK)
- panel_number
- scene_description
- dialogue
- narration
- image_prompt (editable text for image generation)
- status

### pages

- id (UUID)
- chapter_id (FK)
- page_number
- panel_ids (ordered list; which panels appear on this page, partitioned from panels by the Go API using a configurable maximum panels per page)
- draft_image_url
- draft_image_job_id (FK to generation_jobs)
- draft_approved_at
- line_art_image_url
- line_art_image_job_id (FK to generation_jobs)
- line_art_approved_at

### colour_palettes

- id (UUID)
- page_id (FK)
- suggestion_job_id (FK to generation_jobs; the AI palette suggestion job)
- palette (JSONB; array of {region_name, hex_color, label})
- user_modified_at (null if only AI suggestion was applied)

### colour_regions

- id (UUID)
- page_id (FK)
- region_label
- fill_colour_hex
- fill_applied_at
- fill_applied_by (user | ai_auto)

### stage_approvals

- id (UUID)
- project_id (FK)
- entity_type (chapter_outline | character_reference | draft_page | line_art_page | colour_page)
- entity_id (UUID; FK to the relevant record)
- approved_by_user_id (FK)
- approved_at
- credit_estimate_at_approval (NUMERIC)

### generation_jobs

- id (UUID)
- project_id (FK)
- user_id (FK)
- job_type (script_analysis | chapter_outline | panel_text | character_reference | draft_sketch | final_line_art | colour_suggestion | pdf_assembly)
- stage (draft | line_art | colour | reference | text | export)
- model_id
- status (pending | running | done | failed | cancelled)
- idempotency_key (unique; prevents double-charge on retry)
- estimated_cost (NUMERIC)
- actual_cost (NUMERIC)
- hashed_user_id (stored; used on image API call)
- error_message
- created_at
- completed_at

### credit_ledger

- id (UUID)
- user_id (FK)
- project_id (FK)
- generation_job_id (FK)
- transaction_type (debit | refund | adjustment)
- amount (NUMERIC)
- balance_after (NUMERIC)
- created_at

### exports

- id (UUID)
- project_id (FK)
- file_url
- format (pdf | png_zip)
- status
- provenance_metadata_preserved (boolean; must be true before export is marked complete)
- created_at

The database must use foreign keys.

The system must use transactions for all credit-ledger operations.

---

## 10. API Requirements

The backend must provide the following endpoints.

### Projects

```http
POST   /api/v1/projects
GET    /api/v1/projects
GET    /api/v1/projects/:id
PATCH  /api/v1/projects/:id
DELETE /api/v1/projects/:id
```

### Character Management

```http
GET    /api/v1/projects/:id/characters
PATCH  /api/v1/characters/:id
POST   /api/v1/characters/:id/reference-image
POST   /api/v1/characters/:id/approve
```

### Story Generation

```http
POST   /api/v1/projects/:id/analyze
POST   /api/v1/projects/:id/chapters
POST   /api/v1/chapters/:id/panels
```

### Chapter Management

```http
GET    /api/v1/projects/:id/chapters
PATCH  /api/v1/chapters/:id
POST   /api/v1/chapters/:id/approve
```

### Panel Management

```http
GET    /api/v1/chapters/:id/panels
PATCH  /api/v1/panels/:id
```

### Page Management

```http
GET    /api/v1/chapters/:id/pages
PATCH  /api/v1/pages/:id
```

### Draft Sketch Stage

```http
POST   /api/v1/pages/:id/draft
POST   /api/v1/pages/:id/draft/approve
```

### Final Line Art Stage

```http
POST   /api/v1/pages/:id/line-art
POST   /api/v1/pages/:id/line-art/approve
```

### Colouring

```http
GET    /api/v1/pages/:id/colour-palette
POST   /api/v1/pages/:id/colour-palette/suggest
PATCH  /api/v1/pages/:id/colour-palette
POST   /api/v1/pages/:id/colour/approve
```

### Generation Jobs and Credits

```http
POST   /api/v1/projects/:id/estimate
GET    /api/v1/generation-jobs/:id
POST   /api/v1/generation-jobs/:id/cancel   (answers outcome: cancelled | settling | finished)
GET    /api/v1/me/credit-usage
```

### Export

```http
POST   /api/v1/projects/:id/export
GET    /api/v1/exports/:id
GET    /api/v1/exports/:id/download
```

The backend must validate every request.

The backend must enforce strict ownership and authorization checks:
- Project, chapter, character, page, and panel endpoints require that the resource belongs to a project owned by the authenticated user.
- Credit usage (`GET /api/v1/me/credit-usage`) returns credit usage for the authenticated session user only. It also carries `provider_spend` (`enabled`, `cap_usd`, `spent_usd`, `remaining_usd`, `calls`, `uncertain_calls`): the provider spend against the personal spend cap, which is not per user. It is omitted when the AI service cannot answer and reads `enabled: false` when no cap is set.
- `GET /readyz` on the Go API and on the AI service reports whether dependencies can do work (503 while one cannot), distinct from the `GET /healthz` liveness check.
- Generation job endpoints (`GET /api/v1/generation-jobs/:id` and `POST /api/v1/generation-jobs/:id/cancel`) must verify that the requested job belongs to the authenticated user.
- Export endpoints (`GET /api/v1/exports/:id` and `GET /api/v1/exports/:id/download`) must verify that the export belongs to a project owned by the authenticated user.

---

## 11. Security, Compliance and Access Control

### 11.1 General Security

The system must:

- Store API keys in environment variables or a secret manager.
- Never expose API keys to the frontend.
- Validate uploaded files.
- Limit file size.
- Validate file types.
- Protect user projects.
- Restrict download access.
- Prevent unauthorized credit operations.
- Apply rate limits.
- Log generation failures.

The system must not allow users to access another user's project, jobs, or credit usage.

### 11.2 Age Verification and Geo-Blocking (Deferred)

- **Age gate (deferred):** Enforced age gating and date-of-birth blocking are removed from the build scope; a warning that users must be 18+ (the threshold may be 21, to be confirmed with the owner when added) will be introduced in a future release.
- **Geo-blocking (deferred):** No India geo-block and no region restrictions are enforced, as the owner is in India and users can be global.

### 11.3 Hashed User ID

Every call to the image generation API must include a hashed identifier of the requesting user, as required by Meta's API terms.

The hash must be:

- A SHA-256 hash of the user's internal UUID.
- Computed on the backend.
- Included in the API request.
- Stored on the generation job record.

The raw user UUID must not be sent to the API.

### 11.4 AI-Generation Disclosure and Provenance

The system must:

- Include an AI-generation disclosure statement in every export.
- Preserve Meta's provenance watermarks and C2PA metadata in all generated images through every processing step and through export.
- Not strip, modify, or obscure provenance metadata.
- Log whether provenance metadata was preserved for every export (the `exports.provenance_metadata_preserved` column must be `true` before an export is marked complete).

### 11.5 Accounts and Paywall Readiness

The product will eventually charge users. The following extension points must be present in the initial release without any billing implementation:

- A `plans` table and a `plan_id` on `users` (see section 9).
- A `plans.entitlements` JSONB column to hold per-plan feature flags and limits.
- The Go backend must check the user's plan entitlements before allowing a generation job to be created (the check is a pass-through for the free plan).
- A credit quota system per user (see section 5.5).

No payment processor integration is required in the first release.

---

## 12. Non-Functional Requirements

### Performance

- The frontend must remain responsive during generation.
- Generation must run in background workers.
- The system must show job progress.
- The system must support multiple generation jobs.

### Reliability

- Jobs must survive worker restarts.
- Failed jobs must have a clear status.
- Credit operations must be transactional.
- Completed images must be stored persistently.
- Idempotent jobs must not double-charge on retry.

### Maintainability

- The AI model layer must be modular (ports and adapters).
- The backend must use clear service boundaries.
- The system must use structured logs.
- The system must support model replacement without rewriting the application.
- The OpenAPI contract between Go and Python must be the single source of truth for the inter-service interface.

### Scalability

- Workers must be independently scalable.
- The system must support multiple users.
- Image generation must use a queue.
- The system must support configurable concurrency.
- The image job queue concurrency must be tuned to stay within 150 requests per minute per team.

---

## 13. MVP Scope

The first release must include:

- Script input
- Chapter selection
- Chapter outline generation and approval
- Character profiles and approved reference images
- Panel text generation and editing
- Draft sketch generation and approval (one image per page)
- Finished page generation and approval (painted by default, ink by explicit choice)
- Colouring with AI palette suggestion, manual editing, and paint-bucket fill
- Credit estimation per stage
- User approval gates before each image generation stage
- Per-user quotas and global kill switch
- Comic book editor
- PDF export with AI-generation disclosure and preserved provenance metadata
- Download
- Accounts and plan/entitlement extension point (no billing)

The first release must not include:

- Video generation
- Social media publishing
- Public comic book marketplace
- Fine-tuning
- Collaborative editing
- Advanced animation
- Mobile applications
- Payment processor integration
- Enforced age gate and geo-blocking (deferred)

---

## 14. Acceptance Criteria

The project is complete when:

1. A user can create a project.
2. A user can submit a script.
3. The system generates character profiles and reference images.
4. The user can approve character reference images.
5. A user can select the chapter count.
6. The system generates a chapter outline.
7. The user can approve the outline.
8. The system generates panel text for each chapter.
9. The user can edit panel text.
10. The system partitions panels into pages using a configurable maximum panels per page.
11. The system generates one draft sketch image per page.
12. The user can approve or regenerate draft sketches.
13. The system displays an estimated cost before each image generation stage.
14. The user can approve or decline the cost estimate.
15. The system generates one finished painted page per page by default; ink remains an explicit option.
16. The user can approve or regenerate the selected finish.
17. For ink pages, the system suggests a colour palette via AI.
18. For ink pages, the user can edit the palette and apply colours with the paint-bucket tool.
19. The user can undo fill actions on the page canvas.
20. The system records credit usage for every image generation job.
21. The system enforces per-user quotas and the global kill switch.
22. Age warning (18+/21+) and geo-blocking are deferred; no regional restriction or date-of-birth gate is enforced.
23. The system exports a PDF with AI-generation disclosure and provenance metadata preserved.
24. The user can download the PDF.
25. The system uses Meta models only.
26. The system does not expose model API keys.
27. The system does not generate images before user approval of each stage.
28. The system does not silently charge for unapproved generation.
29. The product runs end-to-end with fake adapters and no API key in development and test.
30. The system preserves character consistency using reference image anchoring.

---

## 15. Development Phases

### Phase 1: Script and Character Pipeline

Build:

- Script input
- Character profile extraction
- Character reference image generation (via image adapter)
- Character approval

Success:

The user can submit a script, review character profiles, and approve character reference images.

### Phase 2: Chapter and Outline Pipeline

Build:

- Chapter selection
- Script analysis
- Chapter outline
- Outline approval

Success:

The user can select chapters and receive an approved chapter outline referencing approved characters.

### Phase 3: Panel Text and Draft Sketch Pipeline

Build:

- Panel text generation
- Panel text editing
- Page partitioning from panels (configurable maximum panels per page)
- Draft sketch generation (one image per page)
- Draft approval

Success:

The user can review draft page compositions before selecting a finished-page style.

### Phase 4: Finished Page Pipeline

Build:

- Finished page generation (painted default or explicit ink)
- Selected finish approval

Success:

The user can generate and approve a finished painted page per page by default, with ink available as an explicit option.

### Phase 5: Colouring Pipeline

Build:

- AI colour palette suggestion
- Manual palette editing
- Paint-bucket fill tool with gap-closing
- Manual gap-closer tool
- Canvas stroke/fill undo
- Colour approval

Success:

The user can colour each page using AI palette suggestions and manual fill.

### Phase 6: Comic Book Editor and Export

Build:

- Panel editor
- Chapter editor
- Dialogue editor
- Image regeneration
- Comic book preview
- PDF generation with disclosure and provenance
- PNG export
- Download

Success:

The user can edit the complete comic book and download a PDF.

### Phase 7: Video Generation

**Deferred.** Build this phase only after the comic book workflow is stable and a suitable Meta video API is available.

---

## 16. Important Engineering Rules

- Do not generate images before the user approves each stage.
- Do not generate all chapters in one large request.
- Generate images chapter by chapter.
- Reuse approved character reference images.
- Track the cost of every generation.
- Require approval before paid generation.
- Use background workers for image generation.
- Store project metadata in PostgreSQL.
- Generated images are Meta-hosted (seam stays configurable); our own storage holds approved working copies and export PDFs.
- Keep the AI model layer independent (ports and adapters).
- Use Meta models only.
- Do not build video generation in the first release.
- All credit-ledger writes must be transactional.
- All image generation jobs must be idempotent.
- Enforce per-user quotas and the global kill switch on the backend.
- Pre-moderate all prompts before dispatching to AI.
- Preserve Meta provenance metadata through export.
- Include the hashed user ID on every image API call.
- Meta API keys come only after the whole project is built on fakes, with the Meta adapters inert behind the ports.
- Delivery uses direct-PR with GitHub Actions CI and no external validation pipeline.
- The paint-bucket fill tool must work without any API call.

## 17. Definition of Done

The first version is complete when a user can:

Provide a script -> Approve character references -> Select chapters -> Approve outline -> Edit panel text -> Approve draft sketches -> Approve painted pages (or explicit ink) -> Colour ink pages when selected -> Export a PDF -> Download the comic book.

The system must provide a reliable, cost-controlled, and approval-gated workflow from script to comic book.

The system must run end-to-end with fake adapters in development and test with no network access and no API key.
