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

/**
 * Checks if an approval is stale compared to the entity's latest update/activity timestamp.
 * Matches backend domain.IsApprovalStale(approvedAt, latestActivity).
 * SPEC 3.2, 3.5, 3.6, 3.7; ARCHITECTURE 5.3.
 *
 * @param {string|Date|null|undefined} approvedAt ISO timestamp or Date of approval
 * @param {string|Date|null|undefined} updatedAt ISO timestamp or Date of latest activity
 * @returns {boolean} true if approvedAt is present AND strictly older than updatedAt
 */
export function isApprovalStale(approvedAt, updatedAt) {
	if (!approvedAt || !updatedAt) return false;
	const approvedTime = new Date(approvedAt).getTime();
	const updatedTime = new Date(updatedAt).getTime();
	if (Number.isNaN(approvedTime) || Number.isNaN(updatedTime)) return false;
	return approvedTime < updatedTime;
}

/**
 * Checks if an entity has a valid, non-stale approval.
 *
 * @param {string|Date|null|undefined} approvedAt ISO timestamp or Date of approval
 * @param {string|Date|null|undefined} updatedAt ISO timestamp or Date of latest activity
 * @returns {boolean} true if approvedAt exists, is a valid date, AND is not stale
 */
export function hasValidApproval(approvedAt, updatedAt) {
	if (!approvedAt) return false;
	const approvedTime = new Date(approvedAt).getTime();
	if (Number.isNaN(approvedTime)) return false;
	return !isApprovalStale(approvedAt, updatedAt);
}

/**
 * Formats API errors into structured, user-facing error notices.
 * Handles:
 * - 400 stale approvals
 * - 402 or quota limit errors
 * - 503 kill switch / service unavailable
 * - moderation rejections
 * - 401 unauthenticated
 *
 * @param {Error|string|object} err
 * @returns {{ type: string, title: string, message: string, action: string, status: number }}
 */
export function formatApiError(err) {
	if (!err) return null;
	const status = err.status || (err.response && err.response.status) || 0;
	const rawMsg = typeof err === 'string' ? err : (err.message || 'Unknown error');
	const lower = rawMsg.toLowerCase();

	// 1. Stale Approval / Approval Gate Error (400)
	if (
		lower.includes('stale') ||
		lower.includes('was modified after approval') ||
		lower.includes('regenerated after approval') ||
		lower.includes('must be approved') ||
		lower.includes('approval gate') ||
		lower.includes('prerequisite')
	) {
		return {
			type: 'stale_approval',
			title: 'Approval Gate / Stale Approval Error',
			message: rawMsg,
			action: 'An upstream stage, character, or outline was modified after approval. Re-approve the prerequisite item before continuing.',
			status: status || 400
		};
	}

	// 2. Kill Switch Active (503)
	if (
		status === 503 ||
		lower.includes('kill switch') ||
		lower.includes('killswitch') ||
		lower.includes('service unavailable') ||
		lower.includes('ai service not available')
	) {
		return {
			type: 'kill_switch',
			title: 'Global Kill Switch Active (503)',
			message: 'Image generation has been halted by the operator kill switch or the AI service is unavailable.',
			action: 'Please wait until the operator resets the kill switch threshold or restart with KILL_SWITCH=false.',
			status: status || 503
		};
	}

	// 3. Quota Exceeded / Payment Required (402 or 429)
	if (
		status === 402 ||
		status === 429 ||
		lower.includes('quota') ||
		lower.includes('limit reached') ||
		lower.includes('credits') ||
		lower.includes('payment required')
	) {
		return {
			type: 'quota_exceeded',
			title: 'Usage Quota / Balance Exceeded (402/429)',
			message: rawMsg,
			action: 'Your daily or monthly image generation quota has been reached, or account balance is insufficient.',
			status: status || 402
		};
	}

	// 4. Content Moderation Rejection (400 / 422)
	if (
		lower.includes('moderation') ||
		lower.includes('blocked') ||
		lower.includes('banned') ||
		lower.includes('safety') ||
		lower.includes('content policy')
	) {
		return {
			type: 'moderation_rejected',
			title: 'Content Policy Rejection',
			message: rawMsg,
			action: 'The prompt or script contains terms flagged by AI safety moderation. Please adjust your text.',
			status: status || 400
		};
	}

	// 5. Unauthenticated / Unauthorized (401)
	if (status === 401 || lower.includes('unauthorized') || lower.includes('authentication required')) {
		return {
			type: 'unauthorized',
			title: 'Authentication Required (401)',
			message: 'You are currently unauthenticated. The Go API requires valid credentials.',
			action: 'Enable dev mode in frontend configuration or provide a valid auth token.',
			status: 401
		};
	}

	// Generic error fallback
	return {
		type: 'generic',
		title: status ? `API Error (${status})` : 'API Error',
		message: rawMsg,
		action: 'Please check your connection and try again.',
		status: status || 500
	};
}
