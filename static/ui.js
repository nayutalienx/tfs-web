// Small progressive enhancements. No dependencies or build step.
(() => {
  'use strict';
  const thumbnails = [...document.querySelectorAll('.rich img.screenshot')];
  if (!thumbnails.length || !window.HTMLDialogElement) return;

  const dialog = document.createElement('dialog');
  dialog.className = 'image-viewer';
  dialog.setAttribute('aria-label', 'Просмотр скриншота');
  // Only fixed application markup; untrusted text never enters innerHTML.
  dialog.innerHTML = `<div class="image-toolbar">
    <button type="button" data-action="prev" aria-label="Предыдущий скриншот">←</button>
    <button type="button" data-action="next" aria-label="Следующий скриншот">→</button>
    <span class="image-position"></span>
    <button type="button" data-action="out" aria-label="Уменьшить">−</button>
    <span class="image-scale"></span>
    <button type="button" data-action="in" aria-label="Увеличить">+</button>
    <button type="button" data-action="fit">Вписать</button>
    <button type="button" data-action="actual">100%</button>
    <a class="button secondary" target="_blank" rel="noopener noreferrer">Оригинал ↗</a>
    <button type="button" data-action="close" aria-label="Закрыть просмотр">Закрыть · Esc</button>
  </div><div class="image-help">Перетаскивание — перемещение · Ctrl + колесо — масштаб · ← / → — скриншоты</div>
  <div class="image-status" role="status"></div><div class="image-viewport"><img alt="Скриншот" draggable="false"></div>`;
  document.body.append(dialog);
  const viewport = dialog.querySelector('.image-viewport');
  const image = viewport.querySelector('img');
  const status = dialog.querySelector('.image-status');
  let index = 0, scale = 1, fitMode = true, drag = null;

  function resize(next) {
    if (!image.naturalWidth) return;
    const x = (viewport.scrollLeft + viewport.clientWidth / 2) / scale;
    const y = (viewport.scrollTop + viewport.clientHeight / 2) / scale;
    scale = Math.max(0.05, Math.min(8, next));
    image.style.width = `${image.naturalWidth * scale}px`;
    image.style.height = `${image.naturalHeight * scale}px`;
    viewport.scrollLeft = x * scale - viewport.clientWidth / 2;
    viewport.scrollTop = y * scale - viewport.clientHeight / 2;
    dialog.querySelector('.image-scale').textContent = `${Math.round(scale * 100)}%`;
  }
  function fit() {
    fitMode = true;
    if (!image.naturalWidth) return;
    resize(Math.min(1, (viewport.clientWidth - 24) / image.naturalWidth,
      (viewport.clientHeight - 24) / image.naturalHeight));
    viewport.scrollTo(0, 0);
  }
  function show(next) {
    index = (next + thumbnails.length) % thumbnails.length;
    fitMode = true;
    image.hidden = true;
    status.textContent = 'Загрузка…';
    dialog.querySelector('.image-position').textContent = `${index + 1} / ${thumbnails.length}`;
    dialog.querySelector('[data-action="prev"]').disabled = thumbnails.length < 2;
    dialog.querySelector('[data-action="next"]').disabled = thumbnails.length < 2;
    dialog.querySelector('a').href = thumbnails[index].src;
    image.src = thumbnails[index].src;
  }
  image.addEventListener('load', () => {
    image.hidden = false;
    status.textContent = `${image.naturalWidth} × ${image.naturalHeight}`;
    fit();
  });
  image.addEventListener('error', () => {
    status.textContent = 'Не удалось загрузить скриншот. Попробуйте открыть оригинал.';
  });
  thumbnails.forEach((thumbnail, i) => {
    const link = thumbnail.closest('a');
    if (!link) return;
    link.addEventListener('click', event => {
      if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
      event.preventDefault();
      dialog.showModal();
      document.body.classList.add('image-viewer-open');
      show(i);
    });
  });
  function act(action) {
    if (action === 'close') dialog.close();
    if (action === 'prev') show(index - 1);
    if (action === 'next') show(index + 1);
    if (action === 'fit') fit();
    if (['in', 'out', 'actual'].includes(action)) {
      fitMode = false;
      resize(action === 'actual' ? 1 : scale * (action === 'in' ? 1.25 : 0.8));
    }
  }
  dialog.addEventListener('click', event => {
    const button = event.target.closest('button[data-action]');
    if (button) act(button.dataset.action);
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener('close', () => {
    document.body.classList.remove('image-viewer-open');
    drag = null;
    viewport.classList.remove('dragging');
  });
  dialog.addEventListener('keydown', event => {
    const actions = { ArrowLeft: 'prev', ArrowRight: 'next', '+': 'in', '=': 'in', '-': 'out', '0': 'fit' };
    if (actions[event.key]) { event.preventDefault(); act(actions[event.key]); }
  });
  viewport.addEventListener('wheel', event => {
    if (!event.ctrlKey) return;
    event.preventDefault();
    fitMode = false;
    resize(scale * (event.deltaY < 0 ? 1.1 : 1 / 1.1));
  }, { passive: false });
  viewport.addEventListener('pointerdown', event => {
    if (event.button !== 0) return;
    drag = { x: event.clientX, y: event.clientY, left: viewport.scrollLeft, top: viewport.scrollTop };
    viewport.setPointerCapture(event.pointerId);
    viewport.classList.add('dragging');
    event.preventDefault();
  });
  viewport.addEventListener('pointermove', event => {
    if (!drag) return;
    viewport.scrollLeft = drag.left - event.clientX + drag.x;
    viewport.scrollTop = drag.top - event.clientY + drag.y;
  });
  for (const name of ['pointerup', 'pointercancel', 'lostpointercapture']) {
    viewport.addEventListener(name, () => { drag = null; viewport.classList.remove('dragging'); });
  }
  window.addEventListener('resize', () => { if (dialog.open && fitMode) fit(); });
})();
