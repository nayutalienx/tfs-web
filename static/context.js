// Native progressive enhancement: credentials are submitted by the normal form,
// never persisted in Web Storage or inserted back into the document.
(() => {
  'use strict';
  const button = document.querySelector('[data-context-button]');
  if (!button || !window.HTMLDialogElement || location.pathname === '/context') return;
  let loading = false;
  button.addEventListener('click', async event => {
    if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    if (loading) return;
    loading = true;
    try {
      const response = await fetch('/context', {cache: 'no-store'});
      if (!response.ok) throw new Error('context unavailable');
      const documentView = new DOMParser().parseFromString(await response.text(), 'text/html');
      const panel = documentView.querySelector('#context-panel');
      if (!panel) throw new Error('context missing');
      const dialog = document.createElement('dialog');
      dialog.className = 'context-dialog';
      dialog.setAttribute('aria-label', 'Настройки контекста TFS');
      const close = document.createElement('button');
      close.type = 'button'; close.textContent = 'Закрыть · Esc';
      close.addEventListener('click', () => dialog.close());
      dialog.append(close, document.importNode(panel, true));
      dialog.addEventListener('close', () => dialog.remove(), {once: true});
      document.body.append(dialog);
      dialog.showModal();
    } catch (_) {
      location.assign('/context');
    } finally { loading = false; }
  });
})();
