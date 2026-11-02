#!/usr/bin/env node
// Read actual modal CSS geometry and distinguish CSS-grid snapping from device-grid snapping.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))
 throw new Error('Run through sh android/tools/run.sh so the browser profile has owned scratch storage.');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__fractional__.cjs'));
const {chromium}=require('playwright'),{PNG}=require('pngjs');
const css=await fs.readFile(path.join(root,'web/src/style.css'),'utf8');
const output=path.join(root,'build/artifacts/android-native');
const composition=process.argv.find(value=>value.startsWith('--composition='))?.slice('--composition='.length)||'css-snapped';
if(!['css-snapped','css-ring','css-half-inset-ring','css-stroke'].includes(composition))throw new Error('Unknown border composition');
await fs.mkdir(output,{recursive:true});
let browser;
try {
 browser=await chromium.launch({channel:'chrome',headless:true});
 const cases=[];
 for(const density of [1,2.625,3])for(const width of [320,360,412])for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width,height:800},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());
   const page=await context.newPage();
   await page.setContent(`<!doctype html><style>${css}</style><style>#root{height:800px;background:var(--elevated)}.image-preview{height:238px;padding:0}.image-preview button{position:absolute;left:50%;top:50%;opacity:0}</style><div id="root"><dialog class="image-preview"><button autofocus>Surface fixture focus</button></dialog></div>`);
   await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;document.querySelector('dialog').showModal();},theme);
   const reference=await page.screenshot(),expected=PNG.sync.read(reference);
   await fs.writeFile(path.join(output,`web-modal-image-surface-${width}-${theme}-dpr${density}.png`),reference);
   const {bounds,paint,devicePaint,variants}=await page.evaluate(({density,physicalWidth,physicalHeight})=>{
    const element=document.querySelector('dialog'),style=getComputedStyle(element),rect=element.getBoundingClientRect();
    const bounds={left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height};
    const paint={left:Math.round(rect.left)*density,top:Math.round(rect.top)*density,right:Math.round(rect.right)*density,bottom:Math.round(rect.bottom)*density,radius:parseFloat(style.borderRadius)*density,border:parseFloat(style.borderWidth)*density};
    const devicePaint={left:Math.round(rect.left*density),top:Math.round(rect.top*density),right:Math.round(rect.right*density),bottom:Math.round(rect.bottom*density)};
    const variants=[];
    for(const variant of ['fractional','device-snapped','css-snapped','css-ring','css-half-inset-ring','css-stroke']) {
     const canvas=document.createElement('canvas');canvas.width=physicalWidth;canvas.height=physicalHeight;
     const c=canvas.getContext('2d',{willReadFrequently:true});
     c.fillStyle=getComputedStyle(document.getElementById('root')).backgroundColor;c.fillRect(0,0,canvas.width,canvas.height);
     c.fillStyle=getComputedStyle(element,'::backdrop').backgroundColor;c.fillRect(0,0,canvas.width,canvas.height);
     const box=variant.startsWith('css-')?paint:variant==='device-snapped'?devicePaint:{left:rect.left*density,top:rect.top*density,right:rect.right*density,bottom:rect.bottom*density};
     const {left,top,right,bottom}=box;
     const round=(inset)=>{c.beginPath();c.roundRect(left+inset,top+inset,right-left-2*inset,bottom-top-2*inset,Math.max(0,paint.radius-inset));};
     if(['css-ring','css-half-inset-ring','css-stroke'].includes(variant)) {
      c.fillStyle=style.backgroundColor;round(variant==='css-half-inset-ring'?paint.border/2:0);c.fill();
      if(variant==='css-stroke') {c.strokeStyle=style.borderColor;c.lineWidth=paint.border;round(paint.border/2);c.stroke();}
      else {round(0);c.roundRect(left+paint.border,top+paint.border,right-left-2*paint.border,bottom-top-2*paint.border,Math.max(0,paint.radius-paint.border));c.fillStyle=style.borderColor;c.fill('evenodd');}
     } else {
      c.fillStyle=style.borderColor;round(0);c.fill();
      c.fillStyle=style.backgroundColor;round(paint.border);c.fill();
     }
     variants.push({variant,png:canvas.toDataURL().split(',')[1]});
    }
    return {bounds,paint,devicePaint,variants};
   },{density,physicalWidth:expected.width,physicalHeight:expected.height});
   await fs.writeFile(path.join(output,`web-modal-image-surface-${width}-${theme}-dpr${density}.json`),JSON.stringify({left:bounds.left*density,top:bounds.top*density,width:bounds.width*density,height:bounds.height*density,radius:paint.radius,paint},null,2)+'\n');
   for(const {variant,png} of variants) {
    const actual=PNG.sync.read(Buffer.from(png,'base64'));
    if(actual.width!==expected.width||actual.height!==expected.height)throw new Error('Control dimensions differ');
    let maximum=0,outsideTolerance=0,straightPixels=0,straightMaximum=0,straightOutsideTolerance=0;
    for(let i=0;i<actual.data.length;i+=4) {
     let difference=0;
     for(let channel=0;channel<4;channel++)difference=Math.max(difference,Math.abs(actual.data[i+channel]-expected.data[i+channel]));
     maximum=Math.max(maximum,difference);if(difference>1)outsideTolerance++;
     const x=i/4%actual.width+.5,y=Math.floor(i/4/actual.width)+.5;
     const vertical=(Math.abs(x-paint.left)<=paint.border+1||Math.abs(x-paint.right)<=paint.border+1)&&y>paint.top+paint.radius+1&&y<paint.bottom-paint.radius-1;
     const horizontal=(Math.abs(y-paint.top)<=paint.border+1||Math.abs(y-paint.bottom)<=paint.border+1)&&x>paint.left+paint.radius+1&&x<paint.right-paint.radius-1;
     if(vertical||horizontal) {
      straightPixels++;straightMaximum=Math.max(straightMaximum,difference);
      if(difference>1)straightOutsideTolerance++;
     }
    }
    cases.push({width,density,theme,variant,bounds,paint,devicePaint,maximum,outsideTolerance,straightPixels,straightMaximum,straightOutsideTolerance});
   }
  } finally {await context.close();}
 }
 const report={browser:browser.version(),purpose:'Actual CSS modal vs same-browser Canvas; CSS-grid/device-grid straight-edge diagnostic, not native/device/full-surface parity',cases};
 await fs.writeFile(path.join(output,'surface-fractional-controls.json'),JSON.stringify(report,null,2)+'\n');
 for(const item of cases)console.log(`${item.width}/DPR${item.density}/${item.theme}/${item.variant}: full max=${item.maximum}, >1=${item.outsideTolerance}; straight max=${item.straightMaximum}, >1=${item.straightOutsideTolerance}/${item.straightPixels}`);
 const snapped=cases.filter(item=>item.variant===composition);
 if(snapped.some(item=>item.straightOutsideTolerance))throw new Error('The original CSS straight edges do not match the snapped control; inspect surface-fractional-controls.json.');
 console.log(`Verified ${snapped.reduce((sum,item)=>sum+item.straightPixels,0)} same-browser ${composition} straight-edge control pixels (<=1 channel unit). Rounded edges remain independently reported.`);
} finally {await browser?.close();}
