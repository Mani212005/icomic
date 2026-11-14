// Where panels sit and where an arrow key or button would take them, from the chapter's pages as the API lists them.

const inOrder = (pages) => [...pages].sort((a, b) => a.page_number - b.page_number);

export function pageOfPanel(pages, panelId) {
	return pages.find((page) => (page.panel_ids || []).includes(panelId)) || null;
}

// One step up or down in reading order, as a move request: { pageId, position }, or null at the very start or
// end, or when the step would leave the panel's page empty (a page is deleted by deleting its panel).
export function moveTarget(chapterPages, panelId, direction) {
	const pages = inOrder(chapterPages);
	const index = pages.findIndex((page) => (page.panel_ids || []).includes(panelId));
	if (index < 0) return null;
	const ids = pages[index].panel_ids;
	const at = ids.indexOf(panelId);
	if (direction === 'up') {
		if (at > 0) return { pageId: pages[index].id, position: at - 1 };
		if (index === 0 || ids.length === 1) return null;
		return { pageId: pages[index - 1].id, position: pages[index - 1].panel_ids.length };
	}
	if (at < ids.length - 1) return { pageId: pages[index].id, position: at + 1 };
	if (index === pages.length - 1 || ids.length === 1) return null;
	return { pageId: pages[index + 1].id, position: 0 };
}
