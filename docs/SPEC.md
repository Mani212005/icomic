# Specs : Icomic

## 1. Project Overview

project name - Icomic

### 1.1 Purpose

Build an AI-powered comic book generator.

The user provides a written script.

The system converts the script into a comic book.

The user can:

- Enter a script.
- Select the number of chapters.
- Select an art style.
- Review the generated story.
- Review and edit comic panels.
- Generate comic book images.
- Download the final comic book.
- Publish the comic book.

The system must use Meta AI models only for AI tasks.

### 1.2 Product Goal

Allow a user to turn a written story into a publishable comic book without drawing skills.

The system must provide control over the story, images, and generation cost.

### 1.3 Target Users

- Writers
- Comic book creators
- Independent authors
- Artists
- Content creators
- Students

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

The main workflow is:

1. User creates a project.
2. User provides a script.
3. System asks how many chapters the user wants.
4. User selects the number of chapters.
5. User selects the comic book style.
6. System analyzes the script.
7. System creates a chapter outline.
8. User reviews the outline.
9. User approves the outline.
10. System creates a panel-by-panel storyboard.
11. User reviews the storyboard.
12. System generates comic book images.
13. User reviews and edits the panels.
14. System assembles the comic book.
15. User downloads the comic book.

The system must not generate all images before the user approves the storyboard.

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
- Save the script to the project.

The system must preserve the original script.

The system must not overwrite the original script during story generation.

### 3.2 Chapter Selection

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

### 3.3 Story Outline

The system must generate a story outline.

Each chapter must contain:

- Chapter number
- Chapter title
- Chapter summary
- Important events
- Characters
- Ending or cliffhanger

The user must be able to edit the outline.

The system must wait for user approval before generating the storyboard.

### 3.4 Character Design

The system must identify the main characters.

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

The system must generate a character reference image.

The user must be able to approve or regenerate the reference image.

Approved character references must be reused during image generation.

The system must attempt to preserve character identity across all chapters.

### 3.5 Comic Panel Generation

The system must convert each chapter into comic panels.

Each panel must contain:

- Panel number
- Scene description
- Character actions
- Camera angle
- Background
- Dialogue
- Narration
- Image generation prompt

The user must be able to edit:

- Panel descriptions
- Dialogue
- Narration
- Character actions
- Image prompts

The system must generate images only after the user approves the storyboard.

### 3.6 Comic Book Editor

The system must provide a comic book editor.

The editor must allow the user to:

- View all chapters.
- View all panels.
- Regenerate an image.
- Edit dialogue.
- Edit narration.
- Change panel order.
- Add a panel.
- Delete a panel.
- Preview the comic book.

The editor must save changes automatically.

The editor must display generation status.

### 3.7 Comic Book Export

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
- Copyright or attribution information, when required

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
| Storyboard generation  | Meta language model        |
| Dialogue generation    | Meta language model        |
| Character descriptions | Meta language model        |
| Image generation       | Meta image model           |
| Image review           | Meta multimodal model      |
| Video generation       | Meta video model, optional |

The system must use the best available Meta model for each task.

The exact model must be configurable.

### 4.3 Model Access

The system must support two model access methods.

1. Meta-hosted API
2. Self-hosted Meta models

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

### 4.4 Model Fallback

The system may use another Meta model if the primary model fails.

The fallback model must support the same task.

The system must not silently switch to a non-Meta model.

---

## 5. Credit and Cost Management

### 5.1 Problem

Image generation consumes credits.

Large scripts can contain many chapters and panels.

Generating all images without user approval can waste credits.

The system must control generation cost.

### 5.2 Generation Estimate

Before image generation, the system must display an estimate.

The estimate must contain:

- Number of chapters
- Number of comic pages
- Number of panels
- Number of images
- Estimated credits
- Estimated cost, when available

The system must calculate the estimate using the selected model's actual pricing or credit usage.

The system must not invent credit prices.

If the model provider does not expose pricing or usage, the system must use a configured estimate and label it as an estimate.

### 5.3 Credit Approval

The system must ask the user to approve the estimated cost.

The user must approve before the system starts image generation.

The user must be able to:

- Approve generation
- Cancel generation
- Reduce the number of panels
- Generate one chapter at a time

The system must not charge for an image that it has not attempted to generate.

### 5.4 Credit Ledger

The system must maintain a credit ledger.

Each generation operation must record:

- User ID
- Project ID
- Generation ID
- Model ID
- Task type
- Number of images
- Input usage
- Output usage
- Estimated cost
- Actual cost
- Status
- Timestamp

The ledger must support:

- Credit deduction
- Credit refunds for eligible failures
- Usage history
- Cost reporting

The system must not rely only on frontend credit calculations.

Credit calculations must happen on the backend.

### 5.5 Generation Limits

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

### 6.2 Story Verification

Before image generation, the user must be able to review:

- Chapter outline
- Character profiles
- Panel descriptions
- Dialogue
- Number of images
- Estimated cost

The system must require approval before image generation.

### 6.3 Image Verification

The system must allow the user to review each generated image.

The user must be able to:

- Approve the image
- Reject the image
- Regenerate the image
- Edit the image prompt

Regeneration must display its additional credit cost.

### 6.4 Final Verification

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

### 6.5 Publishing

The initial version must support downloading the comic book.

The user can publish the downloaded PDF on external platforms.

The system must not claim that a comic book is commercially publishable without checking the relevant model license and publishing platform requirements.

The system must provide:

- AI-generated content disclosure
- Model and asset information
- Copyright information
- License information, when available

The system must not claim that the user owns copyright in all AI-generated content.

The system must show the applicable terms before commercial publishing.

---

## 7. Optional Video Generation

Video generation is not part of the first release.

The system may add video generation later.

The optional feature must allow the user to:

- Select a comic panel.
- Generate a short animated scene.
- Preview the animation.
- Download the animation.

The system must estimate video generation costs before generation.

The system must require user approval.

The system must use a Meta video model with confirmed access.

The system must not assume that research models are available through a production API.

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
          |        Python Worker
          |             |
          |             v
          |        Meta Models
          |             |
          +-------------+
                        |
                        v
                 Generated Images
                        |
                        v
                  Comic Editor
                        |
                        v
                    PDF Export
```

### 8.3 Go Backend

The Go backend must handle:

- User authentication
- Project management
- Script uploads
- Chapter management
- Credit management
- Generation requests
- Job status
- Download links
- Publishing metadata

The Go backend must not run long image generation requests directly.

It must create jobs and return a job ID.

### 8.4 Python AI Service

The Python service must handle:

- Script analysis
- Story generation
- Chapter generation
- Storyboard generation
- Character generation
- Image prompt generation
- Image generation
- Image review
- PDF assembly

The service must expose internal APIs.

The service must not expose model API keys to the frontend.

### 8.5 Background Jobs

Image generation must use background jobs.

The system must support:

- Job creation
- Job execution
- Job retries
- Job cancellation
- Job status
- Failed job recovery

The system must not lose a job when a worker restarts.

---

## 9. Database Design

The initial database must contain the following tables.

### users

- id
- email
- password_hash
- created_at

### projects

- id
- user_id
- title
- original_script
- chapter_count
- art_style
- status
- created_at
- updated_at

### chapters

- id
- project_id
- chapter_number
- title
- summary
- status

### characters

- id
- project_id
- name
- description
- reference_image_url
- visual_attributes

### Panels

- id
- chapter_id
- panel_number
- scene_description
- dialogue
- narration
- image_prompt
- image_url
- status

### generation_jobs

- id
- project_id
- user_id
- job_type
- model_id
- status
- estimated_cost
- actual_cost
- error_message
- created_at
- completed_at

### credit_ledger

- id
- user_id
- project_id
- generation_job_id
- transaction_type
- amount
- balance_after
- created_at

### exports

- id
- project_id
- file_url
- format
- status
- created_at

The database must use foreign keys.

The system must use transactions for credit operations.

---

## 10. API Requirements

The backend must provide the following endpoints.

### Projects

```http
POST   /api/projects
GET    /api/projects
GET    /api/projects/:id
PATCH  /api/projects/:id
DELETE /api/projects/:id
```

### Story Generation

```http
POST   /api/projects/:id/analyze
POST   /api/projects/:id/chapters
POST   /api/chapters/:id/storyboard
```

### Character Management

```http
GET    /api/projects/:id/characters
PATCH  /api/characters/:id
POST   /api/characters/:id/reference-image
```

### Panel Management

```http
GET    /api/chapters/:id/panels
PATCH  /api/panels/:id
POST   /api/panels/:id/generate-image
POST   /api/panels/:id/regenerate-image
```

### Generation

```http
POST   /api/projects/:id/estimate
POST   /api/projects/:id/generate
GET    /api/generation-jobs/:id
POST   /api/generation-jobs/:id/cancel
```

### Export

```http
POST   /api/projects/:id/export
GET    /api/exports/:id
GET    /api/exports/:id/download
```

The backend must validate every request.

The backend must verify project ownership.

---

## 11. Security Requirements

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

The system must not allow users to access another user's project.

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

### Maintainability

- The AI model layer must be modular.
- The backend must use clear service boundaries.
- The system must use structured logs.
- The system must support model replacement without rewriting the application.

### Scalability

- Workers must be independently scalable.
- The system must support multiple users.
- Image generation must use a queue.
- The system must support configurable concurrency.

---

## 13. MVP Scope

The first release must include:

- Script input
- Chapter selection
- Chapter outline generation
- Character profiles
- Character reference images
- Storyboard generation
- Comic panel generation
- Credit estimation
- User approval
- Image review
- Comic book editor
- PDF export
- Download

The first release must not include:

- Video generation
- Social media publishing
- Public comic book marketplace
- Fine-tuning
- Collaborative editing
- Advanced animation
- Mobile applications

---

## 14. Acceptance Criteria

The project is complete when:

1. A user can create a project.
2. A user can submit a script.
3. A user can select the chapter count.
4. The system generates a chapter outline.
5. The user can approve the outline.
6. The system generates a storyboard.
7. The user can approve the storyboard.
8. The system displays an estimated generation cost.
9. The user can approve the cost.
10. The system generates comic images.
11. The system preserves character consistency as far as the selected model allows.
12. The user can edit panels.
13. The user can regenerate images.
14. The system records credit usage.
15. The system exports a PDF.
16. The user can download the PDF.
17. The system uses Meta models only.
18. The system does not expose model API keys.
19. The system does not generate images before user approval.
20. The system does not silently charge for unapproved generation.

---

## 15. Development Phases

### Phase 1: Script Pipeline

Build:

- Script input
- Chapter selection
- Script analysis
- Chapter outline

Success:

The user can submit a script and receive an editable chapter outline.

### Phase 2: Storyboard Pipeline

Build:

- Character profiles
- Character reference images
- Panel descriptions
- Dialogue generation

Success:

The user can review a complete storyboard before generating comic images.

### Phase 3: Image Pipeline

Build:

- Meta image model integration
- Image prompt generation
- Background image jobs
- Credit estimation
- Credit approval
- Image generation
- Image review

Success:

The user can generate and approve comic panels without uncontrolled credit usage.

### Phase 4: Comic Book Editor

Build:

- Panel editor
- Chapter editor
- Dialogue editor
- Image regeneration
- Comic book preview

Success:

The user can edit the complete comic book.

### Phase 5: Export

Build:

- PDF generation
- PNG export
- Download
- Publishing metadata

Success:

The user can download a complete comic book.

### Phase 6: Video Generation

Build this phase only after the comic book workflow is stable.

Success:

The user can generate short animated scenes from approved comic panels.

---

## 16. Important Engineering Rules

- Do not generate images before storyboard approval.
- Do not generate all chapters in one large request.
- Generate images chapter by chapter.
- Reuse approved character reference images.
- Track the cost of every generation.
- Require approval before paid generation.
- Use background workers for image generation.
- Store generated images in object storage.
- Store project metadata in PostgreSQL.
- Keep the AI model layer independent.
- Use Meta models only.
- Verify model access before implementation.
- Do not build video generation in the first release.

## 17. Definition of Done

The first version is complete when a user can:

Provide a script → Select chapters → Approve a storyboard → Approve generation cost → Generate comic panels → Edit the comic → Export a PDF → Download and publish the comic book.

The system must provide a reliable and cost-controlled workflow from script to comic book.
