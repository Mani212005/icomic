// Keep keyboard navigation inside the open dialog and restore the invoking control.
export function focusDialog(node, onClose) {
  const previous = document.activeElement;
  const controls = () => [...node.querySelectorAll('button, input, select, textarea, a[href], [tabindex="0"]')]
    .filter((element) => !element.disabled);
  (controls().find((element) => element.tagName === 'INPUT') || node).focus();
  function handleKey(event) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      onClose();
    }
    if (event.key === 'Tab') {
      const items = controls();
      const first = items[0];
      const last = items[items.length - 1];
      if (event.shiftKey && (document.activeElement === first || document.activeElement === node)) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    }
  }
  node.addEventListener('keydown', handleKey);
  const oldOverflow = document.body.style.overflow;
  document.body.style.overflow = 'hidden';
  return {
    destroy() {
      node.removeEventListener('keydown', handleKey);
      document.body.style.overflow = oldOverflow;
      previous?.focus();
    }
  };
}
