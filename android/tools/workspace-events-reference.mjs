#!/usr/bin/env bun
// Actual Web socket policy and connected/unauthenticated callbacks, with browser timer coercion.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(path.join(root, 'web/package.json')), {buildSync} = require('esbuild');
const compiled = buildSync({entryPoints: [path.join(root, 'web/src/workspace-stream.ts')], bundle: true, write: false, format: 'cjs', platform: 'node'}).outputFiles[0].text;
const main = fs.readFileSync(path.join(root, 'web/src/main.tsx'), 'utf8');
const start = main.indexOf('        connected: (value) => {', main.indexOf('const stream = subscribeWorkspaceEvents('));
const end = main.indexOf('        expired:', start);
const connectedCode = main.slice(start, end).trim().replace(/,$/, '');
assert.ok(start > 0 && end > start, 'Inspect moved actual-Web workspace handlers');
const noUserStart = main.indexOf('    if (!user) {\n      setOnline([]);');
const noUserEnd = main.indexOf('      return;', noUserStart);
assert.ok(noUserStart > 0 && noUserEnd > noUserStart);
const noUser = main.slice(noUserStart + '    if (!user) {'.length, noUserEnd);
const settle = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
const action = (type, value) => value === undefined ? {type} : {type, value};
const frame = (kind, value) => ({type: 'frame', kind, value});
const online = [{id: 'bob', devices: 1}];
const cases = [];
for (const random of [0, .5, .999]) {
  const delay = base => Math.trunc(base + random * base * .2);
  const definitions = [
    {id: 'backoff-cap-and-ready-reset', steps: [action('open'), frame('ready', true), frame('presence', online),
      ...[500, 1000, 2000, 4000, 8000, 16000, 30000, 30000].flatMap(base => [action('error'), action('advance', delay(base)), action('open')]),
      frame('ready', true), action('error'), action('advance', delay(500))]},
    {id: 'heartbeat-rearms-watchdog-and-resume-refreshes', steps: [action('open'), frame('ready', true), frame('presence', online), action('advance', 44999),
      frame('ping', true), action('advance', 44999), action('resume'), action('advance', 1)]},
    {id: 'resume-bypasses-disconnected-backoff', steps: [action('open'), frame('ready', true), frame('presence', online), action('resume'), action('error'), action('resume'),
      action('open'), frame('ready', true), frame('presence', online)]},
    {id: 'handshake-watchdog', steps: [action('advance', 45000), action('advance', delay(500)), action('open'), frame('ready', true)]},
    {id: 'policy-close-expires-and-rejects-late-frames', steps: [action('open'), frame('ready', true), frame('presence', online), action('policy'),
      frame('presence', [{id: 'stale'}]), action('resume'), action('advance', 60000)]},
    {id: 'synchronous-open-failure', blocked: true, steps: [action('block', false), action('advance', delay(500)), action('open'), frame('ready', true)]},
    {id: 'late-old-socket-cannot-clear-new-state', steps: [action('open'), frame('ready', true), action('error'), action('advance', delay(500)), action('open'),
      frame('ready', true), frame('presence', online), {type: 'old-frame', kind: 'presence', value: [{id: 'stale'}]}, action('old-error')]},
  ];
  for (const definition of definitions) {
    let now = 100000, sequence = 0, authRequests = 0, blocked = !!definition.blocked, connected = null, presence = [], expired = 0;
    const sockets = [], timers = new Map(), received = [], controls = [];
    class Clock extends Date { static now() { return now; } }
    const timer = (fn, milliseconds) => { const id = ++sequence; timers.set(id, {fn, at: now + Math.trunc(milliseconds)}); return id; };
    const module = {exports: {}};
    vm.runInNewContext(compiled, {module, exports: module.exports, AbortController, crypto: globalThis.crypto, Date: Clock,
      Math: Object.assign(Object.create(Math), {random: () => random}), setTimeout: timer, clearTimeout: id => timers.delete(id)});
    const callbacks = vm.runInNewContext(`let cancelled=false,realtimeConnected=false;({${connectedCode},expired:()=>{${noUser}}})`, {
      setMessagingConnected: value => { connected = value; }, setOnline: value => { presence = value; }, directInbox: {reset() {}},
    });
    const stream = module.exports.subscribeWorkspaceEvents({
      navigation: value => received.push(['navigation', value]), agents: value => received.push(['agents', value]), profiles: value => received.push(['profiles', value]),
      presence: value => { presence = value; }, connected: callbacks.connected, expired: () => { expired++; callbacks.expired(); },
    }, {
      open: () => {
        if (blocked) throw new Error('Blocked fixture');
        const socket = {readyState: 0, close() { this.closed = true; }, send(raw) { const value = JSON.parse(raw); if (!['hello', 'pong'].includes(value.type)) controls.push(value.type); }};
        sockets.push(socket); return socket;
      }, csrf: () => 'fixture', visible: () => true, checkAuth: async () => { authRequests++; },
    });
    const state = () => JSON.parse(JSON.stringify({connections: sockets.length, connected, presence, expired, authRequests, received, controls,
      timers: [...timers.values()].map(item => item.at - now).sort((a, b) => a - b)}));
    await settle(); const records = [{type: 'initial', expected: state()}];
    for (const step of definition.steps) {
      const current = sockets.at(-1);
      switch (step.type) {
        case 'open': current.readyState = 1; current.onopen(); break;
        case 'frame': case 'old-frame': (step.type === 'old-frame' ? sockets[0] : current).onmessage({data: JSON.stringify({type: step.kind, data: step.value})}); break;
        case 'error': current.onerror(); break;
        case 'old-error': sockets[0].onerror(); break;
        case 'policy': current.onclose({code: 1008}); break;
        case 'resume': stream.resume(); break;
        case 'block': blocked = step.value; break;
        case 'advance': {
          const target = now + step.value;
          while (true) {
            const next = [...timers.entries()].filter(([, value]) => value.at <= target).sort((a, b) => a[1].at - b[1].at)[0];
            if (!next) break;
            now = next[1].at; timers.delete(next[0]); next[1].fn(); await settle();
          }
          now = target; break;
        }
        default: throw new Error(step.type);
      }
      await settle(); records.push({...step, expected: state()});
    }
    stream.close(); cases.push({id: definition.id, random, blocked: !!definition.blocked, records});
  }
}
const contents = JSON.stringify({purpose: 'Actual Web workspace-stream.ts and main.tsx connected/unauthenticated callbacks; integer browser timer conversion, control sends exclude Go-owned hello/pong. Not real transport/device parity.', cases}, null, 2) + '\n';
const target = path.join(root, 'android/app/src/test/resources/web-workspace-events.json');
if (process.argv.includes('--check')) assert.equal(fs.readFileSync(target, 'utf8'), contents, 'Web workspace lifecycle changed');
else fs.writeFileSync(target, contents);
console.log(`Verified ${cases.length} actual-Web workspace sequences / ${cases.reduce((sum, item) => sum + item.records.length, 0)} states, three jitter vectors.`);
