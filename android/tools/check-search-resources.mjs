#!/usr/bin/env bun
// Pin original UA images, then optionally compare Chrome and APK decoded pixels.
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import fs from 'node:fs';
import {createRequire} from 'node:module';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {parseArgs} from 'node:util';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const {values} = parseArgs({options: {
  'chrome-resources': {type: 'string'}, apk: {type: 'string'}, report: {type: 'string'},
}});
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, 'web/node_modules'), '__search_resources__.cjs'));
const {PNG} = require('pngjs');
const digest = (data, algorithm = 'sha256') => createHash(algorithm).update(data).digest('hex');
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'android/licenses/chromium-search-cancel.json'), 'utf8'));
const sources = manifest.files.map(file => {
  const encoded = fs.readFileSync(path.join(root, file.path)), png = PNG.sync.read(encoded);
  assert.equal(digest(encoded), file.sha256, `Original source SHA256: ${file.path}`);
  assert.equal(digest(Buffer.concat([Buffer.from(`blob ${encoded.length}\0`), encoded]), 'sha1'), file.gitBlobSHA1, `Upstream git blob: ${file.path}`);
  assert.equal(png.width, file.width); assert.equal(png.height, file.height);
  return {...file, png};
});
const same = (a, b) => a.width === b.width && a.height === b.height && a.data.equals(b.data);
const report = {scope: 'Original search-cancel resources only; not a full-frame raster or hardware gate',
  sources: sources.map(({png, ...file}) => ({...file, rgbaSHA256: digest(png.data)}))};

if (values['chrome-resources']) {
  report.chrome = [];
  for (const name of ['chrome_100_percent.pak', 'chrome_200_percent.pak']) {
    const data = fs.readFileSync(path.join(values['chrome-resources'], name));
    assert.equal(data.readUInt32LE(0), 5, 'Expected Chromium DataPack v5');
    const count = data.readUInt16LE(8), aliases = data.readUInt16LE(10), tableEnd = 12 + (count + 1) * 6 + aliases * 4;
    assert.ok(tableEnd <= data.length, 'Resource tables must fit');
    const matches = sources.map(() => []);
    for (let i = 0; i < count; i++) {
      const at = 12 + 6 * i, id = data.readUInt16LE(at), start = data.readUInt32LE(at + 2), end = data.readUInt32LE(at + 8);
      assert.ok(start >= tableEnd && end >= start && end <= data.length, `Valid resource bounds: ${name}/${id}`);
      const encoded = data.subarray(start, end);
      if (encoded.length < 24 || encoded.readUInt32BE(0) !== 0x89504e47 || encoded.readUInt32BE(4) !== 0x0d0a1a0a) continue;
      if (!sources.some(source => encoded.readUInt32BE(16) === source.width && encoded.readUInt32BE(20) === source.height)) continue;
      const png = PNG.sync.read(encoded);
      sources.forEach((source, index) => { if (same(source.png, png)) matches[index].push(id); });
    }
    assert.ok(matches.every(ids => ids.length > 0), `All four original RGBA images must exist in ${name}`);
    report.chrome.push({name, sha256: digest(data), matches: sources.map((source, index) => ({path: source.path, resourceIDs: matches[index]}))});
  }
}

if (values.apk) {
  const apk = path.resolve(values.apk), entries = execFileSync('unzip', ['-Z1', apk], {encoding: 'utf8'}).trim().split('\n');
  report.apk = {path: apk, sha256: digest(fs.readFileSync(apk)), matches: []};
  for (const source of sources) {
    const matches = entries.filter(name => /^res\/drawable-nodpi(?:-v\d+)?\//.test(name) && path.basename(name) === path.basename(source.path));
    assert.equal(matches.length, 1, `One nodpi APK resource: ${source.path}`);
    const png = PNG.sync.read(execFileSync('unzip', ['-p', apk, matches[0]]));
    assert.ok(same(source.png, png), `aapt must preserve original RGBA pixels: ${matches[0]}`);
    report.apk.matches.push({source: source.path, entry: matches[0], rgbaSHA256: digest(png.data)});
  }
  const notices = execFileSync('unzip', ['-p', apk, 'assets/third-party-notices.txt'], {encoding: 'utf8'});
  assert.ok(notices.includes(fs.readFileSync(path.join(root, manifest.license), 'utf8')), 'APK must ship the original Chromium license');
  report.apk.chromiumLicense = true;
}
if (values.report) {
  fs.mkdirSync(path.dirname(path.resolve(values.report)), {recursive: true});
  fs.writeFileSync(values.report, JSON.stringify(report, null, 2) + '\n');
}
console.log(JSON.stringify(report, null, 2));
