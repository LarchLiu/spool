#!/usr/bin/env node
// Execute the actual Web subscription with controlled asynchronous fallback races.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import {createRequire} from 'node:module';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import vm from 'node:vm';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(path.join(root, 'web/package.json')), {buildSync} = require('esbuild');
const entry = path.join(root, 'web/src/chat-stream.ts');
const compiled = buildSync({entryPoints: [entry], bundle: true, write: false, format: 'cjs', platform: 'node'}).outputFiles[0].text;
const module = {exports: {}};
vm.runInNewContext(compiled, {module, exports: module.exports, AbortController});
const {subscribeChatEvents} = module.exports;
const settle = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
const snapshot = version => ({id: 'chat-one', updated_at: version, turns: []});
const step = (type, value) => value === undefined ? {type} : {type, value};
const definitions = [
  {id: 'healthy-sse-and-reconnect', steps: [step('snapshot', snapshot(10)), step('metadata', {id: 'chat-one', title: 'New title'}),
    step('turn', {turn: {id: 'turn-one'}}), step('error'), step('reconnect'), step('snapshot', snapshot(20)), step('close'), step('turn', {turn: {id: 'stale'}}), step('error')]},
  {id: 'one-fallback-across-repeated-errors', steps: [step('error'), step('error'), step('reconnect'), step('error'), step('resolve', snapshot(10)),
    step('reconnect'), step('error'), step('close')]},
  {id: 'failed-fallback-can-retry', steps: [step('error'), step('reject', 'Chat not found'), step('reconnect'), step('error'), step('resolve', snapshot(20)), step('close')]},
  ...['resolve', 'reject'].map(outcome => ({id: `sse-revokes-late-${outcome}`, steps: [step('error'), step('reconnect'), step('snapshot', snapshot(20)),
    step(outcome, outcome === 'resolve' ? snapshot(10) : 'Stale error'), step('close')]})),
  {id: 'leaving-revokes-fallback-and-events', steps: [step('error'), step('close'), step('resolve', snapshot(10)), step('snapshot', snapshot(30)),
    step('metadata', {id: 'chat-one', title: 'Stale'}), step('turn', {turn: {id: 'stale'}}), step('error')]},
  {id: 'metadata-does-not-initialize-history', steps: [step('metadata', {id: 'chat-one', title: 'Early title'}), step('turn', {turn: {id: 'early'}}),
    step('error'), step('resolve', snapshot(20)), step('close')]},
];
const cases = [];
for (const definition of definitions) {
  const listeners = new Map(), reads = [], events = [], errors = [];
  let auth = 0, connected = null;
  const source = {addEventListener: (name, receive) => listeners.set(name, receive), close() {}};
  const close = subscribeChatEvents('chat-one', {
    ...Object.fromEntries(['snapshot', 'metadata', 'turn'].map(name => [name, value => events.push([name, value])])),
    connected: value => { connected = value; }, error: error => errors.push(error.message),
  }, {
    open: url => { assert.equal(url, '/api/chats/chat-one/events'); return source; },
    snapshot: signal => new Promise((resolve, reject) => reads.push({signal, resolve, reject, settled: false})),
    checkAuth: async () => { auth++; },
  });
  const state = () => JSON.parse(JSON.stringify({events, errors, snapshotRequests: reads.length, authRequests: auth, connected}));
  const records = [{type: 'initial', expected: state()}];
  for (const action of definition.steps) {
    switch (action.type) {
      case 'error': source.onerror(); break;
      case 'reconnect': break; // The browser reuses the logical EventSource on reconnect.
      case 'close': close(); break;
      case 'resolve': case 'reject': {
        const read = reads.find(value => !value.settled); assert.ok(read, 'One pending fallback'); read.settled = true;
        action.type === 'resolve' ? read.resolve(action.value) : read.reject(new Error(action.value)); break;
      }
      default: listeners.get(action.type)({data: JSON.stringify(action.value)});
    }
    await settle(); records.push({...action, expected: state()});
  }
  close(); cases.push({id: definition.id, records});
}
const stateModule = {exports: {}};
vm.runInNewContext(buildSync({entryPoints: [path.join(root, 'web/src/chat-state.ts')], bundle: true, write: false, format: 'cjs', platform: 'node'}).outputFiles[0].text,
  {module: stateModule, exports: stateModule.exports});
const main = fs.readFileSync(path.join(root, 'web/src/main.tsx'), 'utf8');
const handlerStart = main.indexOf('      {\n        snapshot: (v) => {', main.indexOf('return subscribeChatEvents('));
const handlerEnd = main.indexOf('\n      {\n        open: (path)', handlerStart);
assert.ok(handlerStart >= 0 && handlerEnd > handlerStart, 'Inspect moved actual-Web chat callbacks before regeneration');
const handlerSource = main.slice(handlerStart, handlerEnd).trim().replace(/,$/, '');
const handlersCode = buildSync({stdin: {contents: `export function handlers(){return ${handlerSource};}`, loader: 'ts'}, write: false, format: 'cjs'}).outputFiles[0].text;
const clone = value => JSON.parse(JSON.stringify(value));
const turn = (id = 'turn-one', status = 'completed') => ({id, status, part_start: 0, input_end: 0, part_end: 0});
const chat = (version = 10, extra = {}) => ({id: 'chat-one', project_id: 'project-one', title: 'Old title', backend: 'codex', model: 'old-model',
  reasoning_effort: 'medium', permission_mode: 'ask', status: 'completed', updated_at: version, can_control: true,
  participants: [{id: 'alice'}], turns: [turn()], transcript: {parts: []}, ...extra});
const turnEvent = extra => ({...chat(20, {title: 'New turn', status: 'running'}), turn: turn('turn-two', 'running'), turns: undefined, ...extra});
const applicationCases = [
  {id: 'initial-snapshot', previous: null, kind: 'snapshot', value: chat(20)},
  {id: 'snapshot-updates-listed-title-and-participants-only', previous: chat(), kind: 'snapshot', value: chat(20, {title: 'Snapshot title', model: 'changed', participants: [{id: 'bob'}]})},
  {id: 'older-snapshot', previous: chat(30), kind: 'snapshot', value: chat(20)},
  {id: 'metadata-resets-missing-optional-fields', previous: chat(10, {automation_chat: true, collaborator_ids: ['alice'], artifacts: ['artifact']}), kind: 'metadata', value: {id: 'chat-one', title: 'Renamed', updated_at: 20}},
  {id: 'metadata-nullish-fields', previous: chat(10, {automation_chat: true, collaborator_ids: ['alice'], artifacts: ['artifact'], can_control: false}), kind: 'metadata',
    value: {id: 'chat-one', title: null, updated_at: 20, participants: null, can_control: null, automation_chat: null, collaborator_ids: null, artifacts: null}},
  {id: 'metadata-other-listed-chat', previous: chat(), kind: 'metadata', value: {id: 'chat-two', title: 'Other title', updated_at: 20}},
  {id: 'metadata-before-initial-snapshot', previous: null, kind: 'metadata', value: {id: 'chat-one', title: 'Early title', updated_at: 20}},
  {id: 'older-metadata', previous: chat(30), kind: 'metadata', value: {id: 'chat-one', title: 'Old metadata', updated_at: 20}},
  {id: 'turn-removes-pending-input', previous: chat(10, {turns: [turn(), turn('pending-local', 'running')]}), kind: 'turn', value: turnEvent({})},
  {id: 'turn-explicit-turn-list', previous: chat(), kind: 'turn', value: turnEvent({turns: [turn('turn-two', 'running')]})},
  {id: 'turn-does-not-revive-a-terminal-turn', previous: chat(10, {turns: [turn('turn-two', 'completed')]}), kind: 'turn', value: turnEvent({})},
  {id: 'turn-before-initial-snapshot', previous: null, kind: 'turn', value: turnEvent({})},
].map(definition => {
  const item = clone(definition), state = {chat: clone(item.previous), chats: [chat(10, {automation_chat: true, collaborator_ids: ['alice'], artifacts: ['artifact']}), chat(10, {id: 'chat-two'})]};
  if (item.previous) state.chats[0] = clone(item.previous);
  const initialChats = clone(state.chats), factory = {exports: {}};
  const setter = key => value => { state[key] = typeof value === 'function' ? value(state[key]) : value; };
  vm.runInNewContext(handlersCode, {module: factory, exports: factory.exports, activeID: {current: 'chat-one'}, chatID: 'chat-one',
    setChat: setter('chat'), setChats: setter('chats'), setConnected() {}, fail() {}, ...stateModule.exports});
  factory.exports.handlers()[item.kind](clone(item.value));
  return {...item, chats: initialChats, expected: clone(state)};
});
const contents = JSON.stringify({purpose: 'Actual Web chat-stream.ts asynchronous subscription and main.tsx event callbacks, not native network or visual/device parity', cases, applicationCases}, null, 2) + '\n';
const target = path.join(root, 'android/app/src/test/resources/web-chat-events.json');
if (process.argv.includes('--check')) assert.equal(fs.readFileSync(target, 'utf8'), contents, 'Web chat subscription behavior drift');
else fs.writeFileSync(target, contents);
console.log(`Verified ${cases.length} actual-Web chat subscription sequences / ${cases.reduce((n, item) => n + item.records.length, 0)} states and ${applicationCases.length} actual main.tsx snapshot/metadata/turn applications.`);

if (process.argv.includes('--browser')) {
  const runtime = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, 'web/node_modules'), '__chat_browser__.cjs'));
  const {chromium} = runtime('playwright');
  const browserCode = buildSync({entryPoints: [entry], bundle: true, write: false, format: 'iife', globalName: 'SpoolChat'}).outputFiles[0].text;
  const requests = [], sockets = new Set();
  const server = http.createServer((request, response) => {
    if (request.url === '/client.js') { response.writeHead(200, {'Content-Type': 'text/javascript'}); response.end(browserCode); return; }
    if (request.url === '/api/chats/chat-one/events') {
      requests.push(performance.now()); response.writeHead(200, {'Content-Type': 'text/event-stream', 'Cache-Control': 'no-store'});
      response.write(`event: snapshot\ndata: ${JSON.stringify(snapshot(requests.length))}\n\n`);
      if (requests.length === 1) setTimeout(() => response.end(), 25);
      return;
    }
    response.writeHead(200, {'Content-Type': 'text/html'}); response.end('<!doctype html><script src="/client.js"></script>');
  });
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  let browser;
  try {
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
    browser = await chromium.launch({executablePath: process.env.SPOOL_CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true});
    const page = await browser.newPage();
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.evaluate(() => {
      window.received = []; window.fallbacks = 0; window.authChecks = 0;
      window.closeChat = SpoolChat.subscribeChatEvents('chat-one', {
        snapshot: value => received.push(value), metadata() {}, turn() {}, connected() {}, error: error => { throw error; },
      }, {open: path => new EventSource(path), snapshot: async () => { fallbacks++; throw new Error('Unexpected fallback'); }, checkAuth: async () => { authChecks++; }});
    });
    await page.waitForFunction(() => window.received.length === 2, null, {timeout: 15000});
    const actual = await page.evaluate(() => { closeChat(); return {snapshots: received, fallbackRequests: fallbacks, authChecks}; });
    assert.equal(actual.fallbackRequests, 0); assert.equal(actual.authChecks, 1); assert.equal(requests.length, 2);
    const retryIntervalMillis = requests[1] - requests[0];
    assert.ok(retryIntervalMillis >= 2900 && retryIntervalMillis < 8000, 'Installed Chrome default retry must be compatible with the native 3000ms timer plus request latency');
    const report = {purpose: 'Actual Web function and Chrome EventSource over an owned loopback fixture; no real auth/server/tsnet/device claim',
      browser: browser.version(), ...actual, streamRequests: requests.length, retryIntervalMillis, nativeRetryTimerMillis: 3000};
    fs.writeFileSync(path.join(root, 'build/artifacts/android-native/web-chat-events-browser.json'), JSON.stringify(report, null, 2) + '\n');
    console.log(JSON.stringify(report, null, 2));
  } finally {
    if (browser) await browser.close();
    server.closeAllConnections(); for (const socket of sockets) socket.destroy();
    await new Promise(resolve => server.close(resolve));
  }
}
