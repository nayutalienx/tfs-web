const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const source = readFileSync(__dirname + '/timings.js', 'utf8');

function setup(ms, readyState = 'complete') {
  const target = {textContent: 'сервер', parentElement: {title: ''}};
  const events = {};
  const timers = [];
  vm.runInNewContext(source, {
    document: {readyState, querySelector: () => target},
    performance: {getEntriesByType: () => [{startTime: 0, loadEventEnd: ms}]},
    window: {addEventListener: (event, handler) => { events[event] = handler; }},
    setTimeout: callback => timers.push(callback),
  });
  return {target, events, flush: () => timers.splice(0).forEach(callback => callback())};
}

test('full browser load timing, milliseconds and seconds', () => {
  for (const [ms, label] of [[142, '142 мс'], [2512, '2.51 с']]) {
    const app = setup(ms);
    app.flush();
    assert.equal(app.target.textContent, label);
  }
});

test('waits for load and preserves server fallback for unavailable measurements', () => {
  const app = setup(512, 'interactive');
  assert.equal(app.target.textContent, 'сервер');
  app.events.load();
  app.flush();
  assert.equal(app.target.textContent, '512 мс');
  for (const ms of [0, NaN, -1]) {
    const unavailable = setup(ms);
    unavailable.flush();
    assert.equal(unavailable.target.textContent, 'сервер');
  }
});

test('back/forward cache does not display historical time as a fresh load', () => {
  const app = setup(512);
  app.flush();
  app.events.pageshow({persisted: true});
  assert.equal(app.target.textContent, 'кэш');
});
