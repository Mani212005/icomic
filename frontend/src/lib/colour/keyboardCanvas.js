// A keyboard cursor uses the same canvas coordinates and tools as pointer input.
export function keyboardCanvas(node, handlers) {
  let x = node.width / 2;
  let y = node.height / 2;
  let drawing = false;
  function point() {
    const rect = node.getBoundingClientRect();
    return { clientX: rect.left + x * rect.width / node.width, clientY: rect.top + y * rect.height / node.height };
  }
  function stop() {
    drawing = false;
    handlers.stop();
    handlers.cursor(null);
  }
  function keydown(event) {
    if ((event.ctrlKey || event.metaKey) && ['z', 'y'].includes(event.key.toLowerCase())) {
      event.preventDefault();
      if (event.key.toLowerCase() === 'y' || event.shiftKey) handlers.redo();
      else handlers.undo();
      return;
    }
    const direction = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[event.key];
    if (!direction && event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    if (direction) {
      x = Math.max(0, Math.min(node.width - 1, x + direction[0] * 10));
      y = Math.max(0, Math.min(node.height - 1, y + direction[1] * 10));
      if (drawing) handlers.move(point());
    } else if (handlers.isBrush()) {
      drawing = !drawing;
      if (drawing) handlers.start(point());
      else handlers.stop();
    } else {
      handlers.apply(point());
    }
    handlers.cursor({ x: x / node.width * 100, y: y / node.height * 100 });
  }
  node.addEventListener('keydown', keydown);
  node.addEventListener('blur', stop);
  return {
    update(value) { handlers = value; },
    destroy() { node.removeEventListener('keydown', keydown); node.removeEventListener('blur', stop); }
  };
}
