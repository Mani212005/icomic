/**
 * Contract constants and schema types for the Icomic API.
 * Corresponds to contracts/schemas/job.schema.json, contracts/schemas/credit.schema.json,
 * and docs/SPEC.md section 10.
 */

export const JOB_TYPES = {
	SCRIPT_ANALYSIS: 'script_analysis',
	CHAPTER_OUTLINE: 'chapter_outline',
	PANEL_TEXT: 'panel_text',
	CHARACTER_REFERENCE: 'character_reference',
	DRAFT_SKETCH: 'draft_sketch',
	FINAL_LINE_ART: 'final_line_art',
	COLOUR_SUGGESTION: 'colour_suggestion',
	PDF_ASSEMBLY: 'pdf_assembly'
};

export const STAGES = {
	SCRIPT: 'script',
	CHARACTERS: 'characters',
	CHAPTERS: 'chapters',
	PANELS: 'panels',
	DRAFT: 'draft',
	LINE_ART: 'line_art',
	COLOUR: 'colour',
	EXPORT: 'export'
};

export const JOB_STATUS = {
	PENDING: 'pending',
	RUNNING: 'running',
	DONE: 'done',
	FAILED: 'failed',
	CANCELLED: 'cancelled'
};

export const EXPORT_FORMATS = {
	PDF: 'pdf',
	PNG: 'png'
};

export const PAGE_SIZES = {
	A4: 'a4',
	US_LETTER: 'us_letter',
	CUSTOM: 'custom'
};

/**
 * Standard unit pricing estimates from SPEC.md Section 1.4 & ARCHITECTURE.md Section 9.2:
 * muse-image-1.0: $0.01 per image attempt.
 * Language/vision calls: $0.002 per analysis/suggestion.
 */
export const STAGE_ESTIMATED_COSTS = {
	script_analysis: 0.002,
	chapter_outline: 0.002,
	panel_text: 0.002,
	character_reference: 0.01,
	draft_sketch: 0.01,
	final_line_art: 0.01,
	colour_suggestion: 0.002,
	pdf_assembly: 0.001
};
