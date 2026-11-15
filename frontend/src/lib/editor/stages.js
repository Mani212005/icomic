import { STAGES } from '../api/types.js';
export const stageList = [
	{ id: STAGES.SCRIPT, label: '1. Script' },
	{ id: STAGES.CHARACTERS, label: '2. Characters' },
	{ id: STAGES.CHAPTERS, label: '3. Outline' },
	{ id: STAGES.PANELS, label: '4. Panels' },
	{ id: STAGES.DRAFT, label: '5. Drafts' },
	{ id: STAGES.LINE_ART, label: '6. Finish' },
	{ id: STAGES.EXPORT, label: '7. Export' }
];
export const quoteStages = ['character_reference', 'draft_sketch', 'final_line_art'];
