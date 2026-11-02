const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const source = readFileSync(__dirname + '/search.js', 'utf8');
const historySource = source.slice(source.indexOf('  // Keep each history entry'), source.indexOf('  // Every select'));

function setup(href, saved, values = {}) {
  const location = new URL(href);
  const events = {};
  const elements = Object.fromEntries(Object.entries({q: '', scope: 'all', iteration: 'DemoProject\\Sprint 1', assignee: '', state: '', type: '', limit: '100', ...values}).map(([key, value]) => [key, {value}]));
  const form = {elements, addEventListener: (name, fn) => { events[name] = fn; }};
  const history = {state: saved, replaceState(state, _, url) { this.state = state; if (url) location.href = url; }};
  vm.runInNewContext(historySource, {document: {querySelector: () => form}, window: {addEventListener: (name, fn) => { events[name] = fn; }}, history, location, URL});
  return {history, location, elements, events};
}

test('pins current sprint without losing applied filters and history state', () => {
  const app = setup('http://example.invalid/backlog?scope=mine&type=PBI&limit=200', {other: 42});
  assert.equal(app.location.searchParams.get('iteration'), 'DemoProject\\Sprint 1');
  assert.equal(app.location.searchParams.get('type'), 'PBI');
  assert.equal(app.location.searchParams.get('limit'), '200');
  assert.equal(app.history.state.other, 42);
  const all = setup('http://example.invalid/backlog?iteration=');
  assert.equal(all.location.searchParams.get('iteration'), '');
});

test('back restores the exact draft per history entry, not a global last filter', () => {
  const app = setup('http://example.invalid/backlog?iteration=');
  Object.assign(app.elements.q, {value: 'пример'});
  app.elements.assignee.value = 'person@example.invalid';
  app.elements.state.value = 'Новый';
  app.elements.type.value = 'PBI';
  app.elements.limit.value = '300';
  app.events.input();
  const restored = setup(app.location.href, app.history.state);
  for (const name of ['q', 'assignee', 'state', 'type', 'limit']) assert.equal(restored.elements[name].value, app.elements[name].value);
  assert.equal(setup('http://example.invalid/backlog?iteration=').elements.q.value, '');
});

test('local fzf persists in URL and is reapplied after reload/back/forward', () => {
  const start = source.indexOf('  function filterCollection(');
  const end = source.indexOf("  document.querySelectorAll('.repo-grid')", start);
  const location = new URL('http://example.invalid/backlog?iteration=&type=PBI&fzf=API');
  const events = {};
  let input;
  const makeElement = tag => ({value: '', addEventListener(name, fn) { this[name] = fn; }, setAttribute() {}, append() {}});
  const parent = {append() {}};
  const rows = ['API Пример', 'Другой'].map(textContent => ({textContent, parentNode: parent, matches: () => false}));
  const container = {querySelectorAll: selector => selector === '.task' ? rows : [], matches: () => true, before() {}};
  const history = {state: {other: 42}, replaceState(state, _, url) { this.state = state; location.href = url; }};
  vm.runInNewContext(source.slice(source.indexOf('  const normalize'), source.indexOf('  const element')) + source.slice(start, end) + '\nfilterCollection(container, ".task", "задачи");', {
    element: tag => { const node = makeElement(tag); if (tag === 'input') input = node; return node; },
    location, URL, history, container,
    window: {addEventListener: (name, fn) => { events[name] = fn; }},
  });
  assert.equal(input.value, 'API');
  assert.equal(rows[1].hidden, true);
  input.value = 'Другой'; input.input();
  assert.equal(location.searchParams.get('fzf'), 'Другой');
  assert.equal(location.searchParams.get('type'), 'PBI');
  input.value = ''; events.pageshow();
  assert.equal(input.value, 'Другой');
  assert.equal(rows[0].hidden, true);
  location.searchParams.set('fzf', 'API'); events.popstate();
  assert.equal(rows[1].hidden, true);
  input.value = ''; input.input();
  assert.equal(location.searchParams.has('fzf'), false);
  assert.equal(history.state.other, 42);
});
