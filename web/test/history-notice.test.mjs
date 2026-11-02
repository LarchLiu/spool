import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(root, 'package.json'));
const postcss = require('postcss');

test('mobile text-only history retry overrides the global quiet icon rule without changing other controls', () => {
  const css = postcss.parse(fs.readFileSync(path.join(root, 'src/style.css'), 'utf8'));
  const rules = [];
  css.walkRules(rule => { if (rule.selector === '.history-older.quiet') rules.push(rule); });
  assert.equal(rules.length, 1);
  assert.ok(rules[0].nodes.some(node => node.prop === 'font-size' && node.value === '10px'));
  let iconRule = false;
  css.walkAtRules('media', media => {
    media.walkRules('.quiet', rule => { iconRule ||= rule.nodes.some(node => node.prop === 'font-size' && node.value === '0'); });
  });
  assert.equal(iconRule, true, 'Existing mobile icon-only quiet controls retain their styling');
});
