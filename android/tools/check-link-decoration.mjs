#!/usr/bin/env bun
// Keep decoration-relative geometry separate from absolute glyph baselines and full-image pixels.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..'),directory=path.join(root,'build/artifacts/android-native');
const reference=JSON.parse(fs.readFileSync(path.join(directory,'web-link-decoration.json'),'utf8'));
const only=process.argv.includes('--offset-thickness-only'),cases=[];
const raster=process.argv.includes('--raster'),images=[];
if(only&&raster)throw new Error('Keep raster and isolated offset modes separate');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__link_ink__.cjs'));
const single=new Set(['plain','bold-parent','bold-child','italic-parent','italic-child','email','chinese','japanese','heading-one','heading-two','heading-three']);
function lineRects(rects) {
 const lines=[];
 for(const rect of rects) {
  let line=lines.find(line=>Math.abs(line.top-rect.top)<.0001&&Math.abs(line.bottom-rect.bottom)<.0001);
  if(line){line.left=Math.min(line.left,rect.left);line.right=Math.max(line.right,rect.right);}
  else lines.push({...rect});
 }
 return lines.sort((a,b)=>a.top-b.top);
}
for(const web of reference.cases)for(const api of [26,35]) {
 const native=JSON.parse(fs.readFileSync(path.join(directory,`native-link-decoration-api${api}-dpr${web.density}-${web.theme}.json`),'utf8'));
 const rgb=web.geometry[0]?.links[0]?.color.match(/^rgb\((\d+), (\d+), (\d+)\)$/);
 if(!rgb)throw new Error('Unexpected actual CSS link color');
 const color='#FF'+rgb.slice(1).map(channel=>Number(channel).toString(16).padStart(2,'0')).join('').toUpperCase();
 const actualRects=web.snapshots.flatMap(snapshot=>snapshot.commands).filter(command=>command.method==='drawRect'&&command.params.paint.color===color).map(command=>command.params.rect);
 if(native.rows.length!==web.geometry.length||actualRects.length<single.size)throw new Error('Missing actual decoration rows or paint operations');
 for(const expected of web.geometry.filter(row=>single.has(row.id)||row.id.startsWith('wrapped'))) {
  const actual=native.rows.find(row=>row.id===expected.id);if(!actual)throw new Error('Missing native row '+expected.id);
  const er=lineRects(actualRects.filter(rect=>rect.top>=expected.y&&rect.top<expected.y+expected.height));
  const ar=lineRects(native.rects.filter(rect=>rect.top>=actual.y&&rect.top<actual.y+actual.height));
  if(er.length!==ar.length||single.has(expected.id)&&er.length!==1||expected.paintedBaselines?.length!==er.length)
   throw new Error(`Unexpected text/decoration line count at ${api}/${web.density}/${expected.id}: native=${ar.length}/${actual.baselines.length}, Web=${er.length}/${expected.paintedBaselines?.length}`);
  if(expected.paintedBaselines[0]!==Math.round(expected.baseline))throw new Error('Actual Web paint no longer matches its previously audited CSS-snapped first baseline');
  for(let line=0;line<er.length;line++) {
   const e=er[line],a=ar[line],d=web.density;
   const nativeBaseline=actual.baselines.find(baseline=>Math.abs(Math.round(baseline/d+3)*d-a.top)<.0001);
   if(nativeBaseline===undefined)throw new Error('Native decoration has no matching actual Layout baseline');
   const absolute=Math.max(...['left','top','right','bottom'].map(key=>Math.abs(a[key]-e[key]*d)));
   // Blink snaps decoration Y in CSS coordinates. Keep that quantization explicit,
   // rather than counting its sub-CSS-pixel raster phase as an offset style change.
   const relative=Math.abs((a.top-Math.round(nativeBaseline/d)*d)-(e.top-expected.paintedBaselines[line])*d);
   const thickness=Math.abs((a.bottom-a.top)-(e.bottom-e.top)*d);
   const horizontal=Math.max(Math.abs(a.left-e.left*d),Math.abs(a.right-e.right*d));
   const baseline=Math.abs(nativeBaseline-expected.paintedBaselines[line]*d);
   const origin=Math.abs(actual.y-expected.y*d),height=Math.abs(actual.height-expected.height*d);
   cases.push({api,density:d,theme:web.theme,id:expected.id,line,absolute,relative,thickness,horizontal,baseline,origin,height,native:a,web:e});
  }
 }
 if(raster) {
  const {PNG}=require('pngjs'),read=name=>PNG.sync.read(fs.readFileSync(path.join(directory,name+'.png')));
  const actual=read(`native-link-decoration-api${api}-dpr${web.density}-${web.theme}`),expected=read(`web-link-decoration-dpr${web.density}-${web.theme}`);
  if(actual.width!==expected.width||actual.height!==expected.height)throw new Error('Full-viewport decoration dimensions differ; never crop or rescale');
  let maximum=0,outsideTolerance=0;
  for(let i=0;i<actual.data.length;i+=4) {
   let difference=0;for(let c=0;c<4;c++)difference=Math.max(difference,Math.abs(actual.data[i+c]-expected.data[i+c]));
   maximum=Math.max(maximum,difference);if(difference>1)outsideTolerance++;
  }
  images.push({api,density:web.density,theme:web.theme,pixels:actual.width*actual.height,maximum,outsideTolerance});
 }
}
fs.writeFileSync(path.join(directory,raster?'link-decoration-raster-check.json':'link-decoration-check.json'),JSON.stringify({purpose:'CSS-snapped decoration-relative position/thickness and separate absolute text geometry/full images; not device parity',cases,images},null,2)+'\n');
const relativeFailures=cases.filter(item=>item.relative>1||item.thickness>.0001),absoluteFailures=cases.filter(item=>item.absolute>1||item.baseline>1||item.horizontal>1||item.origin>1||item.height>1);
console.log(JSON.stringify({cases:cases.length,relativeFailures:relativeFailures.length,absoluteFailures:absoluteFailures.length,maxima:Object.fromEntries(['absolute','relative','thickness','horizontal','baseline','origin','height'].map(key=>[key,Math.max(...cases.map(item=>item[key]))])),images},null,2));
if(raster&&images.some(item=>item.outsideTolerance))throw new Error('Strict full-frame Web/native link raster differs by more than one channel unit; no threshold was widened.');
if(relativeFailures.length||!only&&absoluteFailures.length)throw new Error(`Actual Web/native ${only?'decoration offset/thickness':'absolute text/decoration'} geometry differs; inspect link-decoration-check.json. Full raster remains independent.`);
console.log(`Verified ${cases.length*(only?2:7)} ${only?'decoration offset/thickness':'absolute text/decoration/flow'} values (<=1 physical pixel, exact thickness) at API26/35 and DPR1/2.625/3, both themes. Full-image/device parity remains independent.`);
