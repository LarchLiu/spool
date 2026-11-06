#!/usr/bin/env bun
// Isolate actual CSS borders/shadows from font/OEM rasterization and compare every pixel.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__surface__.cjs'));
const {PNG}=require('pngjs');
const directory=path.join(root,'build/artifacts/android-native');
const shadowOnly=process.argv.includes('--shadow-only');
const straightOnly=process.argv.includes('--straight-only');
if(shadowOnly&&straightOnly)throw new Error('Choose a single isolated audit or the full surface audit');
const surface=process.argv.find(value=>value.startsWith('--surface='))?.slice('--surface='.length)||'composer';
const modal=['modal-appearance','modal-avatar','modal-group','modal-image'].includes(surface);
if(!modal&&!['composer','floating','popup'].includes(surface))throw new Error('Unknown surface fixture');
const densityArgument=process.argv.find(value=>value.startsWith('--density='))?.slice('--density='.length);
const density=densityArgument===undefined?1:Number(densityArgument);
if(![1,2.625,3].includes(density)||densityArgument!==undefined&&surface!=='modal-image')throw new Error('Dense fixtures are available for modal-image at DPR 1/2.625/3');
const suffix=densityArgument===undefined?'':`-dpr${density}`;
const boxes=surface==='composer'?[[64,99,19],[237,227,19]]:surface==='popup'?[[64,80,17],[224,80,13],[384,80,12],[544,80,10]]:[[80,520,18]];
const distance=(x,y,left,top,width,height,radius)=>{
 const dx=Math.abs(x-left-width/2)-width/2+radius,dy=Math.abs(y-top-height/2)-height/2+radius;
 return Math.hypot(Math.max(dx,0),Math.max(dy,0))+Math.min(Math.max(dx,dy),0)-radius;
};
const cases=[];
for(const width of [320,360,412])for(const theme of ['light','dark']) {
 const read=side=>PNG.sync.read(fs.readFileSync(path.join(directory,`${side}-${surface}-surface-${width}-${theme}${suffix}.png`)));
 const native=read('native'),web=read('web');
 let panels=boxes.map(([top,height,radius])=>({left:12,top,width:width-24,height,radius}));
 if(modal) {
  const bounds=side=>JSON.parse(fs.readFileSync(path.join(directory,`${side}-${surface}-surface-${width}-${theme}${suffix}.json`),'utf8'));
  const actual=bounds('native'),expected=bounds('web');
  for(const key of ['left','top','width','height','radius'])if(!Number.isFinite(actual[key])||!Number.isFinite(expected[key])||Math.abs(actual[key]-expected[key])>.51)throw new Error(`Modal ${key} differs: ${width}/${theme}: ${actual[key]} != ${expected[key]}`);
  panels=[expected];
 }
 if(native.width!==web.width||native.height!==web.height)throw new Error(`Surface fixture dimensions differ: ${width}/${theme}`);
 let maximum=0,different=0,outsideTolerance=0,at,shadowPixels=0,shadowMaximum=0,shadowOutsideTolerance=0,straightPixels=0,straightMaximum=0,straightOutsideTolerance=0;
 for(let i=0;i<native.data.length;i+=4) {
  let difference=0;
  for(let channel=0;channel<4;channel++)difference=Math.max(difference,Math.abs(native.data[i+channel]-web.data[i+channel]));
  if(difference)different++;
  if(difference>1)outsideTolerance++;
  if(difference>maximum){maximum=difference;at={x:i/4%native.width,y:Math.floor(i/4/native.width),native:[...native.data.subarray(i,i+4)],web:[...web.data.subarray(i,i+4)]};}
  const x=i/4%native.width+.5,y=Math.floor(i/4/native.width)+.5;
  // Separately audit pixels outside both rounded border boxes, beyond their 1px AA fringe.
  if(panels.every(({left,top,width,height,radius})=>distance(x,y,left,top,width,height,radius)>1)) {
   shadowPixels++;shadowMaximum=Math.max(shadowMaximum,difference);
   if(difference>1)shadowOutsideTolerance++;
  }
  if(panels.some(panel=>{
   const p=panel.paint||{left:Math.round(panel.left/density)*density,top:Math.round(panel.top/density)*density,right:Math.round((panel.left+panel.width)/density)*density,bottom:Math.round((panel.top+panel.height)/density)*density,radius:panel.radius,border:density};
   return ((Math.abs(x-p.left)<=p.border+1||Math.abs(x-p.right)<=p.border+1)&&y>p.top+p.radius+1&&y<p.bottom-p.radius-1)
    ||((Math.abs(y-p.top)<=p.border+1||Math.abs(y-p.bottom)<=p.border+1)&&x>p.left+p.radius+1&&x<p.right-p.radius-1);
  })) {
   straightPixels++;straightMaximum=Math.max(straightMaximum,difference);
   if(difference>1)straightOutsideTolerance++;
  }
 }
 cases.push({width,theme,density,pixels:native.width*native.height,maximum,different,outsideTolerance,at,shadowPixels,shadowMaximum,shadowOutsideTolerance,straightPixels,straightMaximum,straightOutsideTolerance});
}
fs.writeFileSync(path.join(directory,`${surface}-surface-check${suffix}.json`),JSON.stringify(cases,null,2)+'\n');
console.log(JSON.stringify(cases,null,2));
const label=shadowOnly?'exterior-shadow':straightOnly?'straight-edge':'full-surface';
if(cases.some(item=>shadowOnly?item.shadowPixels===0:straightOnly?item.straightPixels===0:item.pixels===0))throw new Error(`Empty ${label} audit`);
if(cases.some(item=>shadowOnly?item.shadowOutsideTolerance:straightOnly?item.straightOutsideTolerance:item.outsideTolerance))throw new Error(`${surface} ${label} raster differs from actual Web by more than one channel unit; inspect ${surface}-surface-check${suffix}.json.`);
console.log(`Verified ${cases.reduce((sum,item)=>sum+(shadowOnly?item.shadowPixels:straightOnly?item.straightPixels:item.pixels),0)} ${surface} ${label} pixels at three widths, ${modal?1:boxes.length} height(s), DPR ${density} and both themes (<=1 channel unit).`);
