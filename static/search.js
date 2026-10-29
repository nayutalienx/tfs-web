// Native fuzzy filtering and accessible pickers. No dependencies, no mutations.
(() => {
  'use strict';
  const normalize = value => value.toLocaleLowerCase('ru').replaceAll('ё', 'е');
  // Ordered subsequences, with bonuses for adjacency, word boundaries and exact substrings.
  function score(text, query) {
    text = normalize(text); query = normalize(query).trim();
    let total = 0;
    for (const term of query.split(/\s+/).filter(Boolean)) {
      let previous = -1, points = 0;
      for (const char of term) {
        const position = text.indexOf(char, previous + 1);
        if (position < 0) return -Infinity;
        points += 10 + (position === previous + 1 ? 12 : 0) + (position === 0 || /[\s/_.\\-]/.test(text[position - 1]) ? 8 : 0);
        points -= (position - previous - 1) * 0.2;
        previous = position;
      }
      total += points + (text.includes(term) ? 30 : 0);
    }
    return total;
  }
  const element = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  let sequence = 0;
  // Shared keyboard interaction for local and remote suggestions.
  function picker(input, choose) {
    const host = element('span', 'fuzzy-picker');
    input.before(host); host.append(input);
    const panel = element('span', 'fuzzy-menu');
    panel.id = `fuzzy-menu-${++sequence}`;
    panel.role = 'listbox'; panel.hidden = true; host.append(panel);
    input.setAttribute('role', 'combobox'); input.setAttribute('aria-autocomplete', 'list');
    input.setAttribute('aria-controls', panel.id); input.setAttribute('aria-expanded', 'false');
    input.autocomplete = 'off';
    let values = [], active = -1;
    const close = () => { panel.hidden = true; input.setAttribute('aria-expanded', 'false'); input.removeAttribute('aria-activedescendant'); };
    function activate(index) {
      active = index;
      [...panel.querySelectorAll('[role=option]')].forEach((node, i) => {
        node.setAttribute('aria-selected', String(i === active));
        if (i === active) { input.setAttribute('aria-activedescendant', node.id); node.scrollIntoView({block: 'nearest'}); }
      });
    }
    function show(rows, message) {
      values = rows.slice(0, 100); active = -1; panel.replaceChildren();
      panel.append(element('span', 'fuzzy-status', message || `${rows.length} совпадений · ↑↓ Enter · Esc`));
      values.forEach((value, i) => {
        const option = element('span', 'fuzzy-option', value.label);
        option.id = `${panel.id}-${i}`; option.role = 'option'; option.setAttribute('aria-selected', 'false');
        if (value.className) option.classList.add(value.className);
        option.addEventListener('pointerdown', event => event.preventDefault());
        option.addEventListener('click', () => { choose(value); close(); }); panel.append(option);
      });
      panel.hidden = false; input.setAttribute('aria-expanded', 'true');
    }
    input.addEventListener('keydown', event => {
      if (event.key === 'Escape') { close(); event.stopPropagation(); return; }
      if (panel.hidden) return;
      if (['ArrowDown', 'ArrowUp'].includes(event.key)) {
        event.preventDefault(); if (values.length) activate((active + (event.key === 'ArrowDown' ? 1 : -1) + values.length) % values.length);
      }
      if (event.key === 'Enter' && active >= 0) { event.preventDefault(); choose(values[active]); close(); }
    });
    input.addEventListener('blur', () => setTimeout(close, 150));
    return {show, close};
  }
  const ranked = (rows, query) => rows.map((row, index) => ({...row, index, score: score(row.label, query)}))
    .filter(row => row.score !== -Infinity).sort((a, b) => b.score - a.score || a.index - b.index);

  // Every select and datalist remains the real form control; JS only adds a searchable UI.
  document.querySelectorAll('select, input[list]').forEach(control => {
    if (control.disabled || control.multiple) return;
    const isSelect = control.tagName === 'SELECT';
    const source = isSelect ? control : control.list;
    if (!source) return;
    const input = isSelect ? element('input', 'fuzzy-select-input') : control;
    if (isSelect) { input.type = 'text'; input.setAttribute('aria-label', control.getAttribute('aria-label') || control.closest('label')?.firstChild.textContent || 'Поиск вариантов'); control.before(input); control.hidden = true; }
    else control.removeAttribute('list');
    const options = () => [...source.options].filter(option => !option.disabled).map(option => ({value: option.value, label: option.textContent || option.value, className: option.dataset.stateClass}));
    const sync = () => { if (isSelect) input.value = control.selectedOptions[0]?.textContent || ''; };
    const ui = picker(input, row => {
      if (input.fuzzyChoose && row.id) { input.fuzzyChoose(row); return; }
      control.value = row.value; if (!isSelect) input.value = row.value; else sync(); control.dispatchEvent(new Event('change', {bubbles: true}));
    });
    const refresh = () => ui.show(ranked(options(), input.value));
    input.addEventListener('input', refresh);
    input.addEventListener('focus', () => { input.select(); ui.show(options()); });
    if (isSelect) {
      sync(); control.addEventListener('change', sync);
      control.addEventListener('invalid', event => { event.preventDefault(); input.focus(); });
      input.addEventListener('blur', sync);
      control.form?.addEventListener('reset', () => setTimeout(sync));
    }
    if (isSelect) input.addEventListener('keydown', event => { if (event.key === 'Enter') event.preventDefault(); });
    if (isSelect && control.name === 'workitem') { input.dataset.ownTask = ''; input.fuzzyUI = ui; }
  });

  // Branch names are loaded once on demand, then fuzzy-filtered locally.
  const repo = location.pathname.match(/^\/repo\/([^/]+)/)?.[1];
  let branchRequest;
  if (repo) document.querySelectorAll('input[name=branch], input[name=source], input[name=target]').forEach(input => {
    let branches = [];
    const ui = picker(input, row => { input.value = row.value; input.dispatchEvent(new Event('change', {bubbles:true})); });
    input.addEventListener('focus', async () => {
      ui.show([], 'Загрузка веток…');
      try {
        branchRequest ||= fetch(`/api/branches/${repo}`).then(response => { if (!response.ok) throw new Error('branches'); return response.json(); }).catch(error => { branchRequest = null; throw error; });
        const data = await branchRequest;
        branches = data.value.map(row => ({label:row.name.replace(/^refs\/heads\//,''), value:row.name.replace(/^refs\/heads\//,'')}));
        if (document.activeElement === input) ui.show(ranked(branches, input.value), `${branches.length} веток · максимум 1000 · можно ввести другое имя`);
      } catch { if (document.activeElement === input) ui.show([], 'Ветки не загрузились — введите имя вручную.'); }
    });
    input.addEventListener('input', () => ui.show(ranked(branches, input.value)));
  });

  // Local filters: no request per keypress, pinned PR groups and tree ancestry stay intact.
  function filterCollection(container, selector, label, tree = false) {
    const rows = [...container.querySelectorAll(selector)];
    if (!rows.length) return;
    const toolbar = element('div', 'fuzzy-filter toolbar');
    const input = element('input'); input.type = 'search'; input.placeholder = `fzf: ${label}…`;
    input.setAttribute('aria-label', `Быстрый поиск: ${label}`);
    const status = element('span', 'muted'); status.role = 'status';
    toolbar.append(input, status); container.before(toolbar);
    const originals = rows.map(node => ({node, text: tree ? node.querySelector('.tree-row').textContent : node.matches('.inline-diff') ? node.querySelector('summary').textContent : node.textContent}));
    function update() {
      const matches = originals.map(row => ({...row, score: score(row.text, input.value)}));
      const matched = new Set(matches.filter(row => row.score !== -Infinity).map(row => row.node));
      rows.forEach(node => { node.hidden = !matched.has(node) && !(tree && [...matched].some(child => node.contains(child))); });
      if (!tree) {
        const parents = new Set(rows.map(node => node.parentNode));
        parents.forEach(parent => matches.filter(row => row.node.parentNode === parent).sort((a, b) => b.score - a.score || rows.indexOf(a.node) - rows.indexOf(b.node)).forEach(row => parent.append(row.node)));
      }
      container.querySelectorAll('.lane').forEach(lane => { lane.hidden = ![...lane.querySelectorAll('.task')].some(row => !row.hidden); });
      status.textContent = `${matched.size} / ${rows.length} · загруженные элементы`;
    }
    input.addEventListener('input', update);
    input.addEventListener('keydown', event => { if (event.key === 'Escape') { input.value = ''; update(); } if (event.key === 'Enter') { event.preventDefault(); rows.find(row => !row.hidden)?.querySelector('a')?.focus(); } });
    update();
  }
  document.querySelectorAll('.repo-grid').forEach(node => filterCollection(node, '.repo-card', 'репозитории'));
  document.querySelectorAll('.board').forEach(node => filterCollection(node, '.task', 'задачи, ID, ответственный'));
  document.querySelectorAll('.file-list').forEach(node => filterCollection(node, '.file-row', 'файлы / ветки'));
  document.querySelectorAll('ul.item-tree').forEach(node => filterCollection(node, 'li', 'дерево задач', true));
  document.querySelectorAll('section').forEach(node => { if ([...node.children].some(child => child.matches('.pr-row'))) filterCollection(node, ':scope > .pr-row', 'PR, автор, репозиторий, ветки'); });
  const diffs = document.querySelector('.inline-diff');
  if (diffs) filterCollection(diffs.parentNode, ':scope > .inline-diff', 'diff: имя файла');

  // Live server-backed text search. Keep forms as no-JS/Enter fallback, cancel stale requests.
  document.querySelectorAll('input[name=q], input[name=task], input[data-own-task]').forEach(input => {
    const form = input.form;
    const taskPicker = input.hasAttribute('data-own-task');
    if (!form || (!taskPicker && form.method.toLowerCase() !== 'get')) return;
    let timer, controller, generation = 0;
    const own = input.name === 'task' || taskPicker;
    const choose = row => {
      if (own) {
        const select = document.querySelector('select[name=workitem]');
        if (select) {
          if (![...select.options].some(option => option.value === String(row.id))) select.add(new Option(row.label, String(row.id)));
          select.value = String(row.id); select.dispatchEvent(new Event('change', {bubbles:true}));
        } else { input.value = row.title; form.requestSubmit(); }
      } else location.assign(row.url);
    };
    // Use the same menu for local select options and live results.
    if (taskPicker) input.fuzzyChoose = choose;
    const ui = taskPicker ? input.fuzzyUI : picker(input, choose);
    const local = [...document.querySelectorAll('.task, #workitems option[value]')].map(node => ({label:node.textContent.trim(), url:node.querySelector('a')?.getAttribute('href'), id:Number(node.value)})).filter(row => row.url || row.id);
    async function run(version) {
      controller = new AbortController();
      const scope = own || (form.querySelector('[name=scope]') && form.elements.scope.value !== 'all') ? 'mine' : 'all';
      try {
        const iteration = form.elements.iteration?.value || '';
        const state = form.elements.state?.value || '';
        const type = form.elements.type?.value || '';
        const assignee = form.elements.assignee?.value || '';
        const response = await fetch(`/api/search?${new URLSearchParams({q:input.value, scope, iteration, state, type, assignee})}`, {signal:controller.signal, credentials:'same-origin'});
        if (!response.ok) throw new Error('search');
        const data = await response.json();
        if (version !== generation || document.activeElement !== input) return;
        const linked = new Set([...document.querySelectorAll('#workitems a[href^="/workitem/"]')].map(a => Number(a.getAttribute('href').split('/').pop())));
        const serverRows = data.items.filter(item => !own || !linked.has(item.id)).map(item => ({...item, label:`#${item.id} · ${item.type} · ${item.state} · ${item.title} · ${item.person}`, className:item.class}));
        serverRows.forEach(row => { if (!local.some(existing => existing.url === row.url || existing.id === row.id)) local.push(row); });
        if (local.length > 500) local.splice(0, local.length - 500);
        const merged = new Map();
        ranked(local, input.value).forEach(row => merged.set(row.url || `/workitem/${row.id}`, row));
        serverRows.forEach(row => merged.set(row.url, row));
        const rows = [...merged.values()];
        rows.sort((a,b) => (score(b.label,input.value) === -Infinity ? -1 : score(b.label,input.value)) - (score(a.label,input.value) === -Infinity ? -1 : score(a.label,input.value)));
        ui.show(rows, `${rows.length} из TFS и загруженных${data.limited ? ' · лимит TFS 100, уточните запрос' : ''} · ↑↓ Enter`);
      } catch (error) { if (error.name !== 'AbortError' && version === generation && document.activeElement === input) ui.show(ranked(local, input.value), 'TFS недоступен — только загруженные варианты.'); }
    }
    input.addEventListener('input', () => {
      clearTimeout(timer); controller?.abort(); const version = ++generation;
      const rows = ranked(local, input.value);
      ui.show(rows, `${rows.length} среди загруженных · поиск в TFS…`);
      if (input.value.trim()) timer = setTimeout(() => run(version), 300); else ui.close();
    });
    input.addEventListener('keydown', event => { if (event.key === 'Escape') { clearTimeout(timer); controller?.abort(); ++generation; } });
    input.addEventListener('blur', () => { clearTimeout(timer); controller?.abort(); ++generation; });
  });
  document.addEventListener('keydown', event => {
    const typing = event.target.matches('input, textarea, select, [contenteditable=true]');
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); document.querySelector('.global-search input[name=q]')?.focus(); }
    else if (event.key === '/' && !typing && !event.ctrlKey && !event.metaKey) { event.preventDefault(); document.querySelector('.fuzzy-filter input, input[name=q]')?.focus(); }
  });
})();
(function () {
  document.querySelectorAll('.wiki-filter').forEach(input => {
    const tree = input.parentElement.querySelector('.wiki-tree');
    if (!tree) return;
    input.addEventListener('input', () => {
      const query = input.value.toLocaleLowerCase('ru').trim();
      const visit = list => {
        let any = false;
        Array.from(list.children).forEach(item => {
          const link = item.querySelector(':scope > a, :scope > details > summary > a');
          const children = item.querySelector(':scope > ul, :scope > details > ul');
          const details = item.querySelector(':scope > details');
          const nested = children ? visit(children) : false;
          const match = !query || (link && link.textContent.toLocaleLowerCase('ru').includes(query));
          item.hidden = !match && !nested;
          if (details) details.open = query ? nested || match : details.dataset.open === 'true';
          any = any || !item.hidden;
        });
        return any;
      };
      visit(tree);
    });
  });
})();
