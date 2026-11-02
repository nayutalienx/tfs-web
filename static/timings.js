(() => {
  const update = () => {
    const target = document.querySelector('[data-page-time]');
    const navigation = performance.getEntriesByType?.('navigation')[0];
    if (!target || !navigation || navigation.loadEventEnd <= 0) return;
    const ms = navigation.loadEventEnd - navigation.startTime;
    if (!Number.isFinite(ms) || ms < 0) return;
    target.textContent = ms < 1000 ? `${Math.round(ms)} мс` : `${(ms / 1000).toFixed(2)} с`;
    target.parentElement.title = 'Полная загрузка страницы: запрос, данные TFS, HTML, скрипты и изображения';
  };
  // loadEventEnd is populated after the load event handlers have returned.
  if (document.readyState === 'complete') setTimeout(update, 0);
  else window.addEventListener('load', () => setTimeout(update, 0), {once: true});
  window.addEventListener('pageshow', event => {
    if (event.persisted) {
      const target = document.querySelector('[data-page-time]');
      if (target) {
        target.textContent = 'кэш';
        target.parentElement.title = 'Страница восстановлена из кэша браузера; время TFS относится к исходной загрузке';
      }
    }
  });
})();
