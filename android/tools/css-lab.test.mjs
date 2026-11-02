import assert from 'node:assert/strict';
import {test} from 'node:test';
import {labMix} from './css-lab.mjs';

test('short Web hex colors produce the same Lab mixes as expanded hex',()=>{
 for(const weight of [0,.18,.5,.82,1])
  assert.equal(labMix('#fff','#b74e2d',weight),labMix('#ffffff','#b74e2d',weight));
 assert.equal(labMix('#fff','#000',.5),'#777777');
});

test('Pierre selection mixes retain their Web colors and endpoints',()=>{
 assert.equal(labMix('#fff','#b74e2d',.82),'#f7dfd6');
 assert.equal(labMix('#2b2b29','#ca7657',.6),'#68483b');
 assert.equal(labMix('#009fff','#000',1),'#009fff');
 assert.equal(labMix('#009fff','#fff',0),'#ffffff');
});

test('invalid colors fail asset generation instead of emitting NaN Java literals',()=>{
 for(const invalid of [undefined,'#12','transparent','#gggggg','rgb(0,0,0)'])
  assert.throws(()=>labMix(invalid,'#fff',.5),/Invalid CSS hex color/);
});
