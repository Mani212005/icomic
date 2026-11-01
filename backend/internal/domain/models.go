package domain

import (
	"time"
)

// User represents an authenticated creator.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	DateOfBirth  time.Time `json:"date_of_birth"`
	CountryCode  string    `json:"country_code"`
	PlanID       string    `json:"plan_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Plan represents an account tier with entitlements.
type Plan struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Entitlements map[string]interface{} `json:"entitlements"`
	CreatedAt    time.Time              `json:"created_at"`
}

// Project represents a comic book project.
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

// Chapter represents a chapter division of the script.
type Chapter struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	ChapterNumber int    `json:"chapter_number"`
	Title         string `json:"title"`
	Summary       string `json:"summary"`
	Status        string `json:"status"`
}

// Character represents an extracted or user-defined character.
type Character struct {
	ID                  string                 `json:"id"`
	ProjectID           string                 `json:"project_id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	ReferenceImageURL   string                 `json:"reference_image_url,omitempty"`
	ReferenceImageJobID string                 `json:"reference_image_job_id,omitempty"`
	VisualAttributes    map[string]interface{} `json:"visual_attributes"`
	ApprovedAt          *time.Time             `json:"approved_at,omitempty"`
}

// Panel represents a single scene description and dialogue.
type Panel struct {
	ID               string `json:"id"`
	ChapterID        string `json:"chapter_id"`
	PanelNumber      int    `json:"panel_number"`
	SceneDescription string `json:"scene_description"`
	Dialogue         string `json:"dialogue"`
	Narration        string `json:"narration"`
	ImagePrompt      string `json:"image_prompt"`
	Status           string `json:"status"`
}

// Page represents a collection of panels partitioned onto a comic page.
type Page struct {
	ID                string     `json:"id"`
	ChapterID         string     `json:"chapter_id"`
	PageNumber        int        `json:"page_number"`
	PanelIDs          []string   `json:"panel_ids"`
	DraftImageURL     string     `json:"draft_image_url,omitempty"`
	DraftImageJobID   string     `json:"draft_image_job_id,omitempty"`
	DraftApprovedAt   *time.Time `json:"draft_approved_at,omitempty"`
	LineArtImageURL   string     `json:"line_art_image_url,omitempty"`
	LineArtImageJobID string     `json:"line_art_image_job_id,omitempty"`
	LineArtApprovedAt *time.Time `json:"line_art_approved_at,omitempty"`
}

// ColourPalette represents an AI suggestion or user-defined palette for a page.
type ColourPalette struct {
	ID              string                   `json:"id"`
	PageID          string                   `json:"page_id"`
	SuggestionJobID string                   `json:"suggestion_job_id,omitempty"`
	Palette         []ColourRegionSuggestion `json:"palette"`
	UserModifiedAt  *time.Time               `json:"user_modified_at,omitempty"`
}

// ColourRegionSuggestion defines a recommended colour for a region.
type ColourRegionSuggestion struct {
	RegionName string `json:"region_name"`
	HexColor   string `json:"hex_color"`
	Label      string `json:"label"`
}

// ColourRegion represents a client-side fill applied to a page line art region.
type ColourRegion struct {
	ID             string    `json:"id"`
	PageID         string    `json:"page_id"`
	RegionLabel    string    `json:"region_label"`
	FillColourHex  string    `json:"fill_colour_hex"`
	FillAppliedAt  time.Time `json:"fill_applied_at"`
	FillAppliedBy  string    `json:"fill_applied_by"` // user | ai_auto
}

// StageApproval represents an explicit user sign-off on a pipeline stage.
type StageApproval struct {
	ID                        string    `json:"id"`
	ProjectID                 string    `json:"project_id"`
	EntityType                string    `json:"entity_type"` // chapter_outline | character_reference | draft_page | line_art_page | colour_page
	EntityID                  string    `json:"entity_id"`
	ApprovedByUserID          string    `json:"approved_by_user_id"`
	ApprovedAt                time.Time `json:"approved_at"`
	CreditEstimateAtApproval float64   `json:"credit_estimate_at_approval"`
}

// GenerationJob represents a background generation request.
type GenerationJob struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	UserID         string     `json:"user_id"`
	JobType        string     `json:"job_type"`
	Stage          string     `json:"stage"`
	ModelID        string     `json:"model_id"`
	Status         string     `json:"status"` // pending | running | done | failed | cancelled
	IdempotencyKey string     `json:"idempotency_key"`
	EstimatedCost  float64    `json:"estimated_cost"`
	ActualCost     *float64   `json:"actual_cost,omitempty"`
	HashedUserID   string     `json:"hashed_user_id"`
	ErrorMessage   *string    `json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// CreditLedger represents an audit entry for credit deductions or refunds.
type CreditLedger struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	ProjectID       string    `json:"project_id"`
	GenerationJobID string    `json:"generation_job_id"`
	TransactionType string    `json:"transaction_type"` // debit | refund | adjustment
	Amount          float64   `json:"amount"`
	BalanceAfter    float64   `json:"balance_after"`
	CreatedAt       time.Time `json:"created_at"`
}

// Export represents an assembled comic artifact.
type Export struct {
	ID                          string    `json:"id"`
	ProjectID                   string    `json:"project_id"`
	FileURL                     string    `json:"file_url"`
	Format                      string    `json:"format"` // pdf | png_zip
	Status                      string    `json:"status"`
	ProvenanceMetadataPreserved bool      `json:"provenance_metadata_preserved"`
	CreatedAt                   time.Time `json:"created_at"`
}
