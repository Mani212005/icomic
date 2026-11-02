package domain

import (
	"time"
)

// User represents a registered system user (SPEC 9).
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	PlanID       string    `json:"plan_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Plan represents an access entitlement tier (SPEC 9, 11.5).
type Plan struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Entitlements map[string]interface{} `json:"entitlements"`
	CreatedAt    time.Time              `json:"created_at"`
}

// Project represents a user comic book creation project (SPEC 9).
type Project struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Title          string    `json:"title"`
	OriginalScript string    `json:"original_script"`
	ChapterCount   int       `json:"chapter_count"`
	ArtStyle       string    `json:"art_style"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Chapter represents a chapter within a project (SPEC 9).
type Chapter struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	ChapterNumber int       `json:"chapter_number"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary"`
	Status        string    `json:"status"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Character represents an extracted character profile and reference image (SPEC 9).
type Character struct {
	ID                  string                 `json:"id"`
	ProjectID           string                 `json:"project_id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	ReferenceImageURL   string                 `json:"reference_image_url,omitempty"`
	ReferenceImageJobID *string                `json:"reference_image_job_id,omitempty"`
	VisualAttributes    map[string]interface{} `json:"visual_attributes"`
	ApprovedAt          *time.Time             `json:"approved_at,omitempty"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

// Panel represents a comic panel description and text (SPEC 9).
type Panel struct {
	ID               string    `json:"id"`
	ChapterID        string    `json:"chapter_id"`
	PanelNumber      int       `json:"panel_number"`
	SceneDescription string    `json:"scene_description"`
	CharacterActions string    `json:"character_actions"`
	CameraAngle      string    `json:"camera_angle"`
	Background       string    `json:"background"`
	Dialogue         string    `json:"dialogue"`
	Narration        string    `json:"narration"`
	ImagePrompt      string    `json:"image_prompt"`
	Status           string    `json:"status"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Page represents a partitioned page containing ordered panels (SPEC 3.5.1, 9).
type Page struct {
	ID                string     `json:"id"`
	ChapterID         string     `json:"chapter_id"`
	PageNumber        int        `json:"page_number"`
	PanelIDs          []string   `json:"panel_ids"`
	DraftImageURL     string     `json:"draft_image_url,omitempty"`
	DraftImageJobID   *string    `json:"draft_image_job_id,omitempty"`
	DraftApprovedAt   *time.Time `json:"draft_approved_at,omitempty"`
	LineArtImageURL   string     `json:"line_art_image_url,omitempty"`
	LineArtImageJobID *string    `json:"line_art_image_job_id,omitempty"`
	LineArtApprovedAt *time.Time `json:"line_art_approved_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ColourPalette represents AI suggested and user modified page colors (SPEC 9).
type ColourPalette struct {
	ID              string                 `json:"id"`
	PageID          string                 `json:"page_id"`
	SuggestionJobID *string                `json:"suggestion_job_id,omitempty"`
	Palette         map[string]interface{} `json:"palette"`
	UserModifiedAt  *time.Time             `json:"user_modified_at,omitempty"`
}

// ColourRegion represents a fill region applied to a page line art layer (SPEC 9).
type ColourRegion struct {
	ID            string    `json:"id"`
	PageID        string    `json:"page_id"`
	RegionLabel   string    `json:"region_label"`
	FillColourHex string    `json:"fill_colour_hex"`
	FillAppliedAt time.Time `json:"fill_applied_at"`
	FillAppliedBy string    `json:"fill_applied_by"`
}

// StageApproval records an explicit user approval gate (SPEC 9, ARCHITECTURE 5.3).
type StageApproval struct {
	ID                       string    `json:"id"`
	ProjectID                string    `json:"project_id"`
	EntityType               string    `json:"entity_type"`
	EntityID                 string    `json:"entity_id"`
	ApprovedByUserID         string    `json:"approved_by_user_id"`
	ApprovedAt               time.Time `json:"approved_at"`
	CreditEstimateAtApproval float64   `json:"credit_estimate_at_approval"`
}

// GenerationJob represents an AI execution job (SPEC 9, ARCHITECTURE 5).
type GenerationJob struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	UserID         string     `json:"user_id"`
	JobType        string     `json:"job_type"`
	Stage          string     `json:"stage"`
	ModelID        string     `json:"model_id"`
	Status         string     `json:"status"`
	IdempotencyKey string     `json:"idempotency_key"`
	EstimatedCost  float64    `json:"estimated_cost"`
	ActualCost     *float64   `json:"actual_cost,omitempty"`
	HashedUserID   string     `json:"hashed_user_id"`
	ErrorMessage   *string    `json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// CreditLedger records transactional debits, refunds, and adjustments (SPEC 9, ARCHITECTURE 5.1).
type CreditLedger struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	ProjectID       string    `json:"project_id"`
	GenerationJobID *string   `json:"generation_job_id,omitempty"`
	TransactionType string    `json:"transaction_type"`
	Amount          float64   `json:"amount"`
	BalanceAfter    float64   `json:"balance_after"`
	CreatedAt       time.Time `json:"created_at"`
}

// Export represents an assembled downloadable publication (SPEC 9, 10).
type Export struct {
	ID                          string    `json:"id"`
	ProjectID                   string    `json:"project_id"`
	FileURL                     string    `json:"file_url"`
	Format                      string    `json:"format"`
	Status                      string    `json:"status"`
	ProvenanceMetadataPreserved bool      `json:"provenance_metadata_preserved"`
	CreatedAt                   time.Time `json:"created_at"`
}

// CostEstimate itemizes estimated credits and costs before generation (SPEC 5.2).
type CostEstimate struct {
	StageName       string   `json:"stage_name"`
	ImageCount      int      `json:"image_count"`
	EstimatedCost   float64  `json:"estimated_cost"`
	Assumptions     []string `json:"assumptions"`
	IsEstimatePrice bool     `json:"is_estimate_price"`
}
