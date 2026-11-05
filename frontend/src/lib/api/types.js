
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

export function isApprovalStale(approvedAt, updatedAt) {
	if (!approvedAt || !updatedAt) return false;
	const approvedTime = new Date(approvedAt).getTime();
	const updatedTime = new Date(updatedAt).getTime();
	if (Number.isNaN(approvedTime) || Number.isNaN(updatedTime)) return false;
	return approvedTime < updatedTime;
}

export function hasValidApproval(approvedAt, updatedAt) {
	if (!approvedAt) return false;
	const approvedTime = new Date(approvedAt).getTime();
	if (Number.isNaN(approvedTime)) return false;
	return !isApprovalStale(approvedAt, updatedAt);
}

export function formatApiError(err) {
	if (!err) return null;
	const status = err.status || (err.response && err.response.status) || 0;
	const rawMsg = typeof err === 'string' ? err : (err.message || 'Unknown error');
	const lower = rawMsg.toLowerCase();

	if (err.code === 'book_budget_reached' || err.code === 'daily_image_limit_reached') {
		return {
			type: 'quota_exceeded',
			title: err.code === 'book_budget_reached' ? 'Book budget reached' : 'Daily limit reached',
			message: err.code === 'book_budget_reached' ? 'Book image budget used.' : 'Try again tomorrow.',
			action: '',
			status: 429
		};
	}

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
			title: 'Approval needed',
			message: rawMsg,
			action: 'Re-approve the changed item to continue.',
			status: status || 400
		};
	}

	if (
		status === 503 ||
		lower.includes('kill switch') ||
		lower.includes('killswitch') ||
		lower.includes('service unavailable') ||
		lower.includes('ai service not available')
	) {
		return {
			type: 'kill_switch',
			title: 'Service unavailable',
			message: 'Image generation unavailable.',
			action: 'Try again later.',
			status: status || 503
		};
	}

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
			title: 'Quota exceeded',
			message: rawMsg,
			action: 'Check your quota or credits.',
			status: status || 402
		};
	}

	if (
		lower.includes('moderation') ||
		lower.includes('blocked') ||
		lower.includes('banned') ||
		lower.includes('safety') ||
		lower.includes('content policy')
	) {
		return {
			type: 'moderation_rejected',
			title: 'Content blocked',
			message: rawMsg,
			action: 'Edit the prompt and try again.',
			status: status || 400
		};
	}

	if (status === 401 || lower.includes('unauthorized') || lower.includes('authentication required')) {
		return {
			type: 'unauthorized',
			title: 'Sign-in required',
			message: 'Sign in to continue.',
			action: 'Sign in.',
			status: 401
		};
	}

	return {
		type: 'generic',
		title: status ? `API Error (${status})` : 'API Error',
		message: rawMsg,
		action: 'Check your connection and retry.',
		status: status || 500
	};
}
