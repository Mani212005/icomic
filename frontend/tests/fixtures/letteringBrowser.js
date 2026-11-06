
		import { mount } from 'svelte';
		import ComicEditor from '/src/lib/editor/ComicEditor.svelte';
		import { MockApiClient } from '/src/lib/api/mockClient.js';
		import lettering from '/tests/fixtures/lettering.json';
		const art = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 900"><rect width="600" height="900" fill="#ece6d8"/><rect x="18" y="18" width="564" height="864" rx="5" fill="#e1e7e3" stroke="#827e73" stroke-width="2"/><path d="M20 580Q160 450 280 590T580 540V880H20Z" fill="#7f9a9a"/><path d="M380 590 415 260 465 260 500 600Z" fill="#e6d9be" stroke="#7b8279" stroke-width="3"/><path d="m400 260 40-50 40 50Z" fill="#916c62"/><path d="M425 280h30v42h-30Z" fill="#e0bd7b"/><path d="M20 680Q120 630 230 690T580 660M20 780Q150 740 300 785T580 760" fill="none" stroke="#a8b9b2" stroke-width="12"/><ellipse cx="190" cy="610" rx="52" ry="85" fill="#536d7d"/><circle cx="190" cy="515" r="30" fill="#aa826b"/><path d="M160 508q0-55 60-10" fill="#554d48"/><path d="m215 555 52-36 20 13-64 57" fill="#536d7d"/><ellipse cx="285" cy="507" rx="20" ry="12" fill="#c4a66e"/><path d="m290 504 26-16-10 21" fill="#c4a66e"/></svg>`;
		const page = {
			id: lettering.input.page_id, chapter_id: 'chap-1', page_number: 2,
			panel_ids: ['overlay-panel'], lettering_mode: 'overlay',
			draft_image_url: `data:image/svg+xml,${encodeURIComponent(art)}`,
			draft_approved_at: '2026-11-06T00:00:01Z', updated_at: '2026-11-06T00:00:00Z'
		};
		const api = new MockApiClient({ letteringFixtures: [{ page, lettering }] });
		// Retain the real baked sample alongside explicit text-free test artwork.
		window.letteringFixture = { api, page, lettering };
		mount(ComicEditor, {
			target: document.querySelector('#fixture'),
			props: { apiClient: api, project: (await api.listProjects())[0], activeStage: 'draft' }
		});
