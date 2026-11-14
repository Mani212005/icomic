// The panel fields the editor shows and changes, in display order: [API field, label, multi-line input].
export const PANEL_FIELDS = [
	['scene_description', 'Scene', true],
	['dialogue', 'Dialogue', true],
	['narration', 'Narration', true],
	['character_actions', 'Actions', true],
	['camera_angle', 'Camera', false],
	['background', 'Background', true],
	['image_prompt', 'Prompt', true]
];

export const emptyPanelForm = () => Object.fromEntries(PANEL_FIELDS.map(([key]) => [key, '']));
