<script>
	import { HEIGHT, pageWidth, bubblePath, shapePath } from './geometry.js';
	import { comic, needsDevanagari, loadDevanagari } from './fonts.js';
	export let layout;
	let devanagari;
	$: if (needsDevanagari(layout) && !devanagari) {
		loadDevanagari().then((font) => devanagari = font);
	}
	export let idPrefix = layout.page_id;
	$: width = pageWidth(layout);
</script>

<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {HEIGHT}" class="lettering-layer" preserveAspectRatio="none" aria-hidden="true">
	{#each layout.bubbles as bubble, index (bubble.utterance_id)}
		{@const body = bubblePath(bubble, width)}
		<g data-utterance={bubble.utterance_id}>
			<defs>
				<clipPath id="{idPrefix}-{index}">
					<path d={body} />
					{#each bubble.thought_tail as shape}<path d={shapePath(shape, width)} />{/each}
				</clipPath>
			</defs>
			<g fill="white" stroke="black" stroke-width={(layout.stroke_width || 0.0013) * HEIGHT * 2} stroke-linejoin="round" clip-path="url(#{idPrefix}-{index})">
				<path d={body} />
				{#each bubble.thought_tail as shape}<path d={shapePath(shape, width)} />{/each}
			</g>
			<g fill="black">
				{#each bubble.lines as line}
					{@const font = line.font_id === 'comic-neue-v1' ? comic : devanagari}
					<g data-font={line.font_id} data-line={line.text}>
						{#if font}
							{#each line.glyphs as glyph}
								<path d={font.paths[glyph.glyph_id]} transform="translate({glyph.x * width} {glyph.y * HEIGHT}) scale({line.font_size * HEIGHT / font.units_per_em} {-line.font_size * HEIGHT / font.units_per_em})" />
							{/each}
						{/if}
					</g>
				{/each}
			</g>
		</g>
	{/each}
</svg>

<style>
	.lettering-layer { position: absolute; inset: 0; width: 100%; height: 100%; pointer-events: none; }
</style>
