#!/usr/bin/env node
// Distinguish CSS/compositing differences from native-only rendering differences.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))
 throw new Error('Run through sh android/tools/run.sh so the browser profile has an owned scratch directory.');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__surface__.cjs'));
const {chromium}=require('playwright'),{PNG}=require('pngjs');
const output=path.join(root,'build/artifacts/android-native');
const css=await fs.readFile(path.join(root,'web/src/style.css'),'utf8');
await fs.mkdir(output,{recursive:true});
let browser;
try {
 browser=await chromium.launch({channel:'chrome',headless:true});
 const cases=[];
 for(const density of [1,3])for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width:360,height:237},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());
   const page=await context.newPage();
   await page.setContent(`<!doctype html><style>${css}</style><style>#root{height:237px;padding:64px 12px 74px;background:var(--elevated)}.composer{height:99px;box-shadow:none}</style><div id="root"><div class="composer"></div></div>`);
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   const expected=PNG.sync.read(await page.screenshot());
   const variants=await page.evaluate(density=>{
    const box=document.querySelector('.composer'),style=getComputedStyle(box),bounds=box.getBoundingClientRect(),results=[];
    for(const variant of ['nested-fills','center-stroke','border-ring','half-inset-background','blink-stroke']) {
     const canvas=document.createElement('canvas');canvas.width=360*density;canvas.height=237*density;
     const c=canvas.getContext('2d',{willReadFrequently:true});c.scale(density,density);
     c.fillStyle=style.backgroundColor;c.fillRect(0,0,360,237);
     const rect=(inset,radius)=>{c.beginPath();c.roundRect(bounds.x+inset,bounds.y+inset,bounds.width-inset*2,bounds.height-inset*2,radius);};
     if(variant==='nested-fills') {
      c.fillStyle=style.borderColor;rect(0,19);c.fill();
      c.fillStyle=style.backgroundColor;rect(1,18);c.fill();
     } else if(variant==='center-stroke'||variant==='blink-stroke') {
      c.fillStyle=style.backgroundColor;rect(variant==='blink-stroke'?.5:0,variant==='blink-stroke'?18.5:19);c.fill();
      c.strokeStyle=style.borderColor;c.lineWidth=1;rect(.5,18.5);c.stroke();
     } else {
      c.fillStyle=style.backgroundColor;rect(variant==='half-inset-background'?.5:0,variant==='half-inset-background'?18.5:19);c.fill();
      c.beginPath();c.roundRect(bounds.x,bounds.y,bounds.width,bounds.height,19);
      c.roundRect(bounds.x+1,bounds.y+1,bounds.width-2,bounds.height-2,18);
      c.fillStyle=style.borderColor;c.fill('evenodd');
     }
     results.push({variant,png:canvas.toDataURL().split(',')[1]});
    }
    return results;
   },density);
   for(const {variant,png} of variants) {
    const actual=PNG.sync.read(Buffer.from(png,'base64'));let maximum=0,outsideTolerance=0;
    if(actual.width!==expected.width||actual.height!==expected.height)throw new Error(`Control dimensions differ at DPR ${density}`);
    for(let i=0;i<actual.data.length;i+=4) {
     let difference=0;for(let channel=0;channel<4;channel++)difference=Math.max(difference,Math.abs(actual.data[i+channel]-expected.data[i+channel]));
     maximum=Math.max(maximum,difference);if(difference>1)outsideTolerance++;
    }
    cases.push({density,theme,variant,maximum,outsideTolerance});
   }
  } finally {await context.close();}
 }
 const report={browser:browser.version(),purpose:'Actual CSS vs same-browser software Canvas controls; not native/device parity',cases};
 await fs.writeFile(path.join(output,'surface-raster-controls.json'),JSON.stringify(report,null,2)+'\n');
 console.log(JSON.stringify(report,null,2));
} finally {await browser?.close();}
