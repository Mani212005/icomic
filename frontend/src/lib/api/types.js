
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
	colour_suggestion: 0.005,
	pdf_assembly: 0.001
};

function timestampMicros(value) {
	if (typeof value !== 'string') return null;
	const match = /^(.*?)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)$/.exec(value);
	if (!match) return null;
	const milliseconds = Date.parse(`${match[1]}${match[3]}`);
	if (!Number.isFinite(milliseconds)) return null;
	const fractionalMicros = Number((match[2] || '').slice(0, 6).padEnd(6, '0'));
	return milliseconds * 1000 + fractionalMicros;
}

export function formatCostQuote(quote) {
	if (!quote || typeof quote.estimated_cost !== 'number' || !Number.isFinite(quote.estimated_cost)) {
		return 'Estimate unavailable';
	}
	const currency = quote.currency || 'USD';
	const amount = new Intl.NumberFormat('en-US', {
		style: 'currency',
		currency,
		minimumFractionDigits: 2,
		maximumFractionDigits: 6
	}).format(quote.estimated_cost);
	return `${quote.price_verified ? 'Estimate' : 'Unverified estimate'}: ${amount}`;
}

export function isApprovalStale(approvedAt, updatedAt) {
	const approvedTime = timestampMicros(approvedAt);
	const updatedTime = timestampMicros(updatedAt);
	if (approvedTime === null || updatedTime === null) return true;
	return approvedTime <= updatedTime;
}

export function hasValidApproval(approvedAt, updatedAt) {
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

	const retryDelay = Number.isFinite(Number(err.retry_after_seconds))
		? Math.max(1, Math.ceil(Number(err.retry_after_seconds)))
		: null;
	if (err.code === 'CONTENT_POLICY_REJECTED') {
		return {
			type: 'moderation_rejected',
			title: 'Review this request',
			message: rawMsg,
			action: 'Edit the request to meet content guidelines, then try again.',
			status: status || 400
		};
	}
	if (err.code === 'RATE_LIMITED' || err.code === 'UPSTREAM_UNAVAILABLE') {
		return {
			type: 'service_unavailable',
			title: 'Generation is temporarily unavailable',
			message: rawMsg,
			action: retryDelay ? `Try again in about ${retryDelay} seconds.` : 'Try again later.',
			status: status || (err.code === 'RATE_LIMITED' ? 429 : 503),
			retryable: err.retryable === true,
			retry_after_seconds: retryDelay
		};
	}
	if (err.code === 'OUTCOME_UNKNOWN') {
		return {
			type: 'generic',
			title: 'Generation status is uncertain',
			message: rawMsg,
			action: 'Check the job status before trying again.',
			status: status || 502
		};
	}
	if (err.code === 'INVALID_PROVIDER_OUTPUT' || err.code === 'SERVICE_CONFIGURATION') {
		return {
			type: 'generic',
			title: 'Generation could not be completed',
			message: rawMsg,
			action: err.code === 'SERVICE_CONFIGURATION' ? 'Contact support.' : 'Try again later.',
			status: status || 502
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
