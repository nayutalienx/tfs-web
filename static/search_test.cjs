// Run with: node --test static/search_test.cjs (Node is only a test tool).
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const source = readFileSync(__dirname + '/search.js', 'utf8');
const start = source.indexOf('  const normalize');
const end = source.indexOf('  const element');
const {score} = vm.runInNewContext(`(() => {${source.slice(start,end)}; return {score};})()`);
test('ordered fuzzy subsequences, Cyrillic, ё and multiple terms', () => {
  assert.ok(Number.isFinite(score('Исправление ошибки сервера', 'исошср')));
  assert.ok(Number.isFinite(score('Учёт времени', 'учет врм')));
  assert.equal(score('abc', 'ca'), -Infinity);
  assert.equal(score('abc', 'missing'), -Infinity);
  assert.equal(score('anything', '  '), 0);
  assert.ok(Number.isFinite(score('#42 demo-api Тестовый пользователь', '42 тст')));
});
test('exact contiguous matches outrank scattered ones', () => {
  assert.ok(score('api', 'api') > score('a_long_path_index', 'api'));
  assert.ok(score('demo-api', 'api') > score('demo_alpha_path_index', 'api'));
});
