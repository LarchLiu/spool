#!/usr/bin/env bun
import { requireRunTemp } from "../../tools/test-scratch.mjs";
requireRunTemp();
// Browser WebCodecs supplies complete composited frames, not a second handwritten expected renderer.
import fs from 'node:fs/promises';
import http from 'node:http';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__animation__.cjs'));
const {chromium}=require('playwright');
const {PNG}=require('pngjs');
const directory=path.join(root,'android/app/src/test/resources/image-animation');
const manifest=JSON.parse(await fs.readFile(path.join(directory,'manifest.json'),'utf8'));
const server=http.createServer((req,res)=>{res.setHeader('Content-Type','text/html');res.end('<!doctype html><title>Local synthetic image decode</title>');});
let browser;
try {
 await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});
 browser=await chromium.launch({channel:'chrome',headless:true});
 const page=await browser.newPage();await page.goto(`http://127.0.0.1:${server.address().port}`);
 const results=[];
 for(const item of manifest) {
  const bytes=await fs.readFile(path.join(directory,item.name));
  results.push(await page.evaluate(async({name,data})=>{
   const decoder=new ImageDecoder({data:Uint8Array.from(atob(data),c=>c.charCodeAt(0)),type:name.endsWith('.gif')?'image/gif':'image/webp'});
   try {
    await decoder.tracks.ready;
    const track=decoder.tracks.selectedTrack,frames=[];
    for(let index=0;index<track.frameCount;index++) {
     const {image}=await decoder.decode({frameIndex:index,completeFramesOnly:true});
     try{const pixels=new Uint8Array(image.allocationSize({format:'RGBA'}));await image.copyTo(pixels,{format:'RGBA'});frames.push({width:image.displayWidth,height:image.displayHeight,duration:image.duration/1000,pixels:btoa(String.fromCharCode(...pixels))});}finally{image.close();}
    }
    const bitmap=await createImageBitmap(new Blob([Uint8Array.from(atob(data),c=>c.charCodeAt(0))],{type:name.endsWith('.gif')?'image/gif':'image/webp'}),{imageOrientation:'from-image'});
    const canvas=new OffscreenCanvas(bitmap.width,bitmap.height),context=canvas.getContext('2d');context.drawImage(bitmap,0,0);const poster={width:bitmap.width,height:bitmap.height,pixels:btoa(String.fromCharCode(...context.getImageData(0,0,bitmap.width,bitmap.height).data))};bitmap.close();
    const element=new Image();element.src=`data:image/${name.endsWith('.gif')?'gif':'webp'};base64,${data}`;await element.decode();poster.elementWidth=element.naturalWidth;poster.elementHeight=element.naturalHeight;
    if(poster.width!==poster.elementWidth||poster.height!==poster.elementHeight)throw new Error(`${name}: ImageBitmap and actual img orientation differ; inspect the browser oracle.`);
    return{name,loops:Number.isFinite(track.repetitionCount)?track.repetitionCount+1:0,frames,poster};
   }finally{decoder.close();}
  },{name:item.name,data:bytes.toString('base64')}));
 }
 const target=path.join(directory,'web-frames.json'),value=JSON.stringify(results,null,2)+'\n';
 if(process.argv.includes('--write'))await fs.writeFile(target,value);
 else if(await fs.readFile(target,'utf8')!==value)throw new Error('Browser animation frames changed; inspect before updating with --write.');
 const output=path.join(root,'build/artifacts/android-native');await fs.mkdir(output,{recursive:true});await fs.writeFile(path.join(output,'web-image-animation.json'),value);
 const playback=[];
 for(const name of ['fast.gif','fast.webp']) {
  const data=await fs.readFile(path.join(directory,name));
  await page.evaluate(({name,data})=>{document.body.style.margin='0';document.body.style.background='#fff';document.body.innerHTML='<img style="display:block" alt="Synthetic animation">';document.querySelector('img').src=`data:image/${name.endsWith('.gif')?'gif':'webp'};base64,${data}`;},{name,data:data.toString('base64')});
  await page.locator('img').evaluate(image=>image.decode());
  const reference=results.find(r=>r.name===name),changes=[];const start=Date.now();
  while(Date.now()-start<1100) {
   const pixels=PNG.sync.read(await page.locator('img').screenshot({animations:'allow'})).data;
   const distances=reference.frames.map(frame=>{const expected=Buffer.from(frame.pixels,'base64');let error=0;for(let i=0;i<pixels.length;i+=4)for(let c=0;c<3;c++)error+=Math.abs(pixels[i+c]-Math.round((expected[i+c]*expected[i+3]+255*(255-expected[i+3]))/255));return error;});
   const frame=distances.indexOf(Math.min(...distances));
   if(!changes.length||changes.at(-1).frame!==frame)changes.push({frame,time:Date.now()-start});
  }
  const visible=changes.slice(1,-1).flatMap((change,index)=>change.frame<2?[changes[index+2].time-change.time]:[]);
  if(visible.length<2||visible.filter(duration=>duration>=50).length<2)throw new Error(`${name}: browser minimum-delay playback was not observed; ${JSON.stringify(changes)}`);
  playback.push({name,changes,minimumDelayObserved:visible});
 }
 await fs.writeFile(path.join(output,'web-image-playback.json'),JSON.stringify(playback,null,2)+'\n');
 await page.evaluate(results=>{
  document.body.innerHTML='<canvas></canvas>';const canvas=document.querySelector('canvas');canvas.width=800;canvas.height=results.length*126;const context=canvas.getContext('2d');context.fillStyle='#eee';context.fillRect(0,0,canvas.width,canvas.height);context.font='14px monospace';
  results.forEach((result,row)=>{context.fillStyle='#222';context.fillText(result.name,8,row*126+18);[...result.frames,...Array(4-result.frames.length).fill(null),result.poster].forEach((frame,index)=>{if(!frame)return;const image=new ImageData(Uint8ClampedArray.from(atob(frame.pixels),c=>c.charCodeAt(0)),frame.width,frame.height);const tile=document.createElement('canvas');tile.width=frame.width;tile.height=frame.height;tile.getContext('2d').putImageData(image,0,0);context.imageSmoothingEnabled=false;context.drawImage(tile,index*160,row*126+24,128,96);});});
 },results);
 await page.locator('canvas').screenshot({path:path.join(output,'web-image-animation-contact.png')});
 console.log(`Verified ${results.length} actual-browser GIF/WebP sequences, ${results.reduce((n,r)=>n+r.frames.length,0)} composited frames and repeat counts.`);
}finally{await browser?.close();await new Promise(resolve=>server.close(resolve));}
