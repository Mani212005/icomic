export function drawColourComposite(context, fills, lineArt) {
	context.globalCompositeOperation = 'source-over';
	context.drawImage(fills, 0, 0);
	context.globalCompositeOperation = 'multiply';
	context.drawImage(lineArt, 0, 0);
}
