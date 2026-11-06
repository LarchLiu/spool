#!/usr/bin/env bun
// Execute actual Web history fetch/merge/scroll functions, including canceled work.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(path.join(root, 'web/package.json')), {buildSync} = require('esbuild');
const compile = file => {
  const module = {exports: {}};
  const text = buildSync({entryPoints: [path.join(root, 'web/src', file)], bundle: true, format: 'cjs', platform: 'node', write: false}).outputFiles[0].text;
  vm.runInNewContext(text, {module, exports: module.exports, AbortController, setTimeout, clearTimeout});
  return module.exports;
};
const {fetchOlderHistory, restoreHistoryScroll} = compile('history-loading.ts');
const {prependHistory, reconcile} = compile('chat-state.ts');
const clone = value => JSON.parse(JSON.stringify(value));
const part = text => ({role: 'assistant', kind: 'text', text});
const turn = (id, text, status = 'completed', extra = {}) => ({turn: {id, status, part_start: 0, input_end: 0, part_end: 1, ...extra}, parts: [part(text)]});
const chat = (entries, extra = {}) => {
  const parts = [], turns = entries.map(entry => {
    const start = parts.length; parts.push(...entry.parts);
    return {...entry.turn, part_start: start, input_end: start + entry.turn.input_end, part_end: parts.length};
  });
  return {id: 'chat-one', title: 'Current title', project_id: 'project-one', backend: 'codex', model: 'current-model', permission_mode: 'ask',
    updated_at: 30, can_control: true, turns, transcript: {source: 'codex-native', parts}, status: turns.at(-1)?.status || 'completed', ...extra};
};
const current = chat([turn('recent', 'Recent'), turn('live', 'Live output is longer', 'running')], {history: {before: 'middle/日本 ?&', has_more: true}});
const page = chat([turn('oldest', 'Oldest'), turn('recent', 'Recent')], {updated_at: 1, title: 'Old title', model: 'old-model', history: {before: 'oldest', has_more: true}});
const definitions = [
  ['prepend-retains-live-metadata-and-authority', current, page],
  ['old-timestamps-and-file-order', chat([turn('recent', 'New', 'completed', {started_at: -1}), turn('pending-local', 'Draft', 'running')]),
    chat([turn('zero', 'First', 'completed', {started_at: 0}), turn('negative', 'Second', 'completed', {started_at: -50})], {history: {has_more: false}})],
  ['running-overlap-cannot-regress-terminal', chat([turn('same', 'Long completed', 'completed'), turn('new', 'Latest', 'running')]),
    chat([turn('older', 'Older'), turn('same', 'Short', 'running')], {transcript: {source: 'metadata', parts: [part('Older'), part('Short')]}, history: {has_more: false}})],
  ['native-turn-alias-rebases-parts', chat([turn('old-id', 'Preserved longer output', 'running', {native_turn_id: 'native-one'}), turn('newer', 'Newest', 'running')]),
    chat([turn('older', 'Older'), turn('new-id', 'Final native', 'completed', {native_turn_id: 'native-one'})], {history: {before: 'next', has_more: true}})],
  ['empty-terminal-page-keeps-current-turns', current, chat([], {history: {has_more: false}})],
  ['different-chat-is-inert', current, {...page, id: 'chat-other'}],
  ['unavailable-page-keeps-live-snapshot', current, chat([turn('older', 'Older')], {history: {error: 'unavailable', has_more: false}})],
  ['longer-running-parts-survive-metadata', current, chat([turn('older', 'Older'), turn('live', 'Live', 'running')], {transcript: {parts: [part('Older'), part('Live')]}, history: {has_more: false}})],
];
const merges = definitions.map(([id, previous, page]) => ({id, previous, page, expected: prependHistory(clone(previous), clone(page))}));
const hydrated = prependHistory(clone(current), clone(page));
const live = chat([turn('recent', 'Recent'), turn('live', 'Live output is even longer now', 'running')], {updated_at: 40, history: {before: 'middle/日本 ?&', has_more: true}});
merges.push({id: 'live-snapshot-after-prepend', previous: hydrated, page: live, mode: 'reconcile', expected: reconcile(clone(hydrated), clone(live))});
const cursor = (before = 'middle/日本 ?&', extra = {}) => ({id: 'chat-one', history: {before, has_more: true, ...extra}});
const fetchDefinitions = [
  {id: 'valid-next-page', from: cursor(), page: cursor('older')},
  {id: 'terminal-page-needs-no-cursor', from: cursor(), page: {id: 'chat-one', history: {has_more: false}}},
  {id: 'missing-cursor-is-inert', from: cursor(''), page: cursor('older')},
  {id: 'unavailable-is-inert', from: cursor('before', {error: 'unavailable'}), page: cursor('older')},
  {id: 'complete-is-inert', from: cursor('before', {has_more: false}), page: cursor('older')},
  {id: 'same-cursor-rejected', from: cursor(), page: cursor()},
  {id: 'empty-next-cursor-rejected', from: cursor(), page: cursor('')},
  {id: 'different-chat-rejected', from: cursor(), page: {...cursor('older'), id: 'other'}},
  {id: 'network-failure', from: cursor(), failure: 'offline'},
  {id: 'cancel-before-paint', from: cursor(), page: cursor('older'), cancel: 'paint'},
  {id: 'cancel-during-read', from: cursor(), page: cursor('older'), cancel: 'read'},
];
const fetches = [];
for (const definition of fetchDefinitions) {
  const controller = new AbortController(); const requests = []; let yields = 0;
  const result = {id: definition.id, from: definition.from, page: definition.page, failure: definition.failure, cancel: definition.cancel};
  try {
    const page = await fetchOlderHistory(definition.from, async (path, signal) => {
      assert.equal(signal, controller.signal); requests.push(path);
      if (definition.cancel === 'read') controller.abort();
      if (definition.failure) throw new Error(definition.failure);
      return definition.page;
    }, controller.signal, async () => { yields++; if (definition.cancel === 'paint') controller.abort(); });
    result.expected = {page, requests, yields};
  } catch (error) { result.expected = {error: error.message, requests, yields}; }
  fetches.push(result);
}
const scrolls = [];
for (const following of [true, false]) for (const anchor of [null, {id: 'chat-one', height: 1200, top: 150}, {id: 'chat-one', height: 3400, top: 1900}])
  for (const height of [600, 1200, 5000]) {
    const element = {scrollTop: 400, scrollHeight: height}; restoreHistoryScroll(element, following, anchor);
    scrolls.push({following, anchor, height, initialTop: 400, expectedTop: element.scrollTop});
  }
const contents = JSON.stringify({purpose: 'Actual Web history-loading.ts and chat-state.ts; no real API/device equivalence claim.', merges, fetches, scrolls}, null, 2) + '\n';
const target = path.join(root, 'android/app/src/test/resources/web-history.json');
if (process.argv.includes('--check')) assert.equal(fs.readFileSync(target, 'utf8'), contents, 'Actual Web history behavior changed');
else fs.writeFileSync(target, contents);
console.log(`Verified actual Web history: ${merges.length} merge, ${fetches.length} fetch/cancel and ${scrolls.length} scroll cases.`);
