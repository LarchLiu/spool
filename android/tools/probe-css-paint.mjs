#!/usr/bin/env node
// Inspect actual Blink paint operations for original CSS, not a Canvas recreation.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))
 throw new Error('Run through sh android/tools/run.sh so the browser profile has an owned scratch directory.');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__surface__.cjs'));
const {chromium}=require('playwright');
const {PNG}=require('pngjs');
const output=path.join(root,'build/artifacts/android-native');
const profile=process.argv.find(value=>value.startsWith('--profile='))?.slice('--profile='.length)||'composer';
if(!['composer','modal-image','modal-appearance'].includes(profile))throw new Error('Unknown CSS paint profile');
const width=Number(process.argv.find(value=>value.startsWith('--width='))?.slice('--width='.length)||360);
if(![320,360,412].includes(width))throw new Error('Unsupported original CSS viewport width');
const modal=profile!=='composer',widthTag=width===360?'':`width${width}-`,tag=(modal?profile+'-':'')+widthTag;
const css=await fs.readFile(path.join(root,'web/src/style.css'),'utf8');
await fs.mkdir(output,{recursive:true});
let browser;
try {
 browser=await chromium.launch({channel:'chrome',headless:true});
 const cases=[];
 for(const density of [1,2.625,3])for(const theme of ['light','dark'])for(const shadow of [false,true]) {
  // A multiple of eight keeps the 21/8-DPR viewport integral without cropping either image.
  const height=modal?800:density===2.625?240:237;
  const context=await browser.newContext({viewport:{width,height},deviceScaleFactor:density});
  try {
   const page=await context.newPage(),client=await context.newCDPSession(page);
   let layers=[];
   client.on('LayerTree.layerTreeDidChange',event=>{if(event.layers)layers=event.layers;});
   await client.send('LayerTree.enable');
   await context.route('**/*',route=>route.abort());
   const contents=modal?`<dialog class="${profile==='modal-image'?'image-preview':'appearance-dialog'}" style="height:238px;padding:0;${shadow?'':'box-shadow:none'}"><button style="position:absolute;left:50%;top:50%;opacity:0">Focus</button></dialog>`:'<div class="composer"></div>';
   await page.setContent(`<!doctype html><style>${css}</style><style>#root{height:${height}px;${modal?'':'padding:64px 12px 74px;'}background:var(--elevated)}.composer{height:99px;${shadow?'':'box-shadow:none'}}</style><div id="root">${contents}</div>`);
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   if(modal)await page.evaluate(()=>{const dialog=document.querySelector('dialog');dialog.showModal();dialog.querySelector('button').focus();});
   const box=await page.locator(modal?'dialog':'.composer').evaluate(element=>{
    const r=element.getBoundingClientRect(),s=getComputedStyle(element);
    return {left:r.left,top:r.top,right:r.right,bottom:r.bottom,radius:parseFloat(s.borderTopLeftRadius),border:parseFloat(s.borderTopWidth),fill:s.backgroundColor,color:s.borderTopColor};
   });
   const screenshot=await page.screenshot(),expected=PNG.sync.read(screenshot);
   await fs.writeFile(path.join(output,`web-css-paint-${tag}dpr${density}-${theme}-${shadow?'shadow':'border'}.png`),screenshot);
   const snapshots=[],replays=[];
   for(const layer of layers.filter(layer=>layer.drawsContent&&layer.width>0&&layer.height>0)) {
    const {snapshotId}=await client.send('LayerTree.makeSnapshot',{layerId:layer.layerId});
    try {
     const {commandLog}=await client.send('LayerTree.snapshotCommandLog',{snapshotId});
     const {dataURL}=await client.send('LayerTree.replaySnapshot',{snapshotId,scale:density});
     const bytes=Buffer.from(dataURL.slice(dataURL.indexOf(',')+1),'base64'),replay=PNG.sync.read(bytes);
     let replayVsScreenshot=null;
     // Modal/backdrop layers are separate composited pictures; never pass a layer crop off as the viewport.
     if(!modal) {
      let maximum=0,outsideTolerance=0;
      if(replay.width!==expected.width||replay.height!==expected.height)throw new Error('Replay bounds differ from the viewport');
      for(let i=0;i<replay.data.length;i+=4) {
       let difference=0;for(let c=0;c<4;c++)difference=Math.max(difference,Math.abs(replay.data[i+c]-expected.data[i+c]));
       maximum=Math.max(maximum,difference);if(difference>1)outsideTolerance++;
      }
      replayVsScreenshot={maximum,outsideTolerance};
     }
     await fs.writeFile(path.join(output,`css-paint-replay-${tag}dpr${density}-${theme}-${shadow?'shadow':'border'}${modal?'-layer'+snapshots.length:''}.png`),bytes);
     const reasons=await client.send('LayerTree.compositingReasons',{layerId:layer.layerId});
     snapshots.push({layer:{...layer},reasons,replay:{width:replay.width,height:replay.height},replayVsScreenshot,commands:commandLog});
     replays.push({dataURL,layer});
    } finally {await client.send('LayerTree.releaseSnapshot',{snapshotId});}
   }
   if(!snapshots.length)throw new Error('No paint snapshots for the actual CSS scene');
   let composedReplay=null;
   if(modal) {
    // This is an explicitly separate software composition of actual layer replays, not a page screenshot.
    const dataURL=await page.evaluate(async ({replays,density,width,height})=>{
     const canvas=document.createElement('canvas');canvas.width=width;canvas.height=height;
     const c=canvas.getContext('2d',{willReadFrequently:true});c.fillStyle='white';c.fillRect(0,0,width,height);
     for(const {dataURL,layer} of replays) {
      const m=layer.transform||[1,0,0,0,0,1,0,0,0,0,1,0,0,0,0,1];
      if(m.length!==16||m.some((value,index)=>![12,13].includes(index)&&value!==([0,5,10,15].includes(index)?1:0)))
       throw new Error('Layer replay composition currently supports only recorded translations: '+JSON.stringify(m));
      const image=new Image();image.src=dataURL;await image.decode();
      c.drawImage(image,(layer.offsetX+m[12])*density,(layer.offsetY+m[13])*density);
     }
     return canvas.toDataURL();
    },{replays,density,width:expected.width,height:expected.height});
    const bytes=Buffer.from(dataURL.slice(dataURL.indexOf(',')+1),'base64'),actual=PNG.sync.read(bytes);
    let maximum=0,outsideTolerance=0;
    if(actual.width!==expected.width||actual.height!==expected.height)throw new Error('Composed replay changed the full viewport dimensions');
    for(let i=0;i<actual.data.length;i+=4) {
     let difference=0;for(let c=0;c<4;c++)difference=Math.max(difference,Math.abs(actual.data[i+c]-expected.data[i+c]));
     maximum=Math.max(maximum,difference);if(difference>1)outsideTolerance++;
    }
    composedReplay={maximum,outsideTolerance};
    await fs.writeFile(path.join(output,`css-paint-composed-replay-${tag}dpr${density}-${theme}-${shadow?'shadow':'border'}.png`),bytes);
   }
   const commands=snapshots.flatMap(snapshot=>snapshot.commands),strokes=commands.filter(command=>command.method==='drawRRect'&&command.params.paint.styleName==='Stroke');
   if(strokes.length!==1)throw new Error('Blink no longer uses one rounded stroke for this CSS border');
   const stroke=strokes[0].params,r=stroke.rrect;
   if(!modal)for(const [key,want] of Object.entries({left:Math.round(box.left)+box.border/2,top:Math.round(box.top)+box.border/2,right:Math.round(box.right)-box.border/2,bottom:Math.round(box.bottom)-box.border/2}))
    if(r[key]!==want)throw new Error('Unexpected Blink border-center '+key);
   for(const key of ['upperLeftRadius','upperRightRadius','lowerRightRadius','lowerLeftRadius'])
    if(r[key].xRadius!==box.radius-box.border/2||r[key].yRadius!==box.radius-box.border/2)throw new Error('Unexpected Blink border-center radius');
   if(stroke.paint.strokeWidth!==box.border)throw new Error('Unexpected Blink border width');
   if(!commands.some(command=>command.method==='drawRRect'&&command.params.paint.styleName==='Fill'&&JSON.stringify(command.params.rrect)===JSON.stringify(r)))
    throw new Error('Blink background no longer follows the same inset border center');
   cases.push({density,theme,shadow,viewport:{width,height},box,composedReplay,snapshots});
  } finally {await context.close();}
 }
 const report={browser:browser.version(),profile,purpose:'Original CSS Blink paint command inspection; not native/device pixel parity',cases};
 await fs.writeFile(path.join(output,`css-paint-commands${modal?'-'+profile:''}${width===360?'':'-width'+width}.json`),JSON.stringify(report,null,2)+'\n');
 if(process.argv.includes('--write-border-fixtures')) {
  if(modal||width!==360)throw new Error('Border fixtures require the original 360px composer profile');
  const directory=path.join(root,'android/app/src/test/resources/composer-border');
  await fs.mkdir(directory,{recursive:true});
  const fixtures=[];
  for(const item of cases.filter(item=>!item.shadow)) {
   const name=`dpr${item.density}-${item.theme}.png`;
   const bytes=await fs.readFile(path.join(output,`web-css-paint-dpr${item.density}-${item.theme}-border.png`));
   await fs.writeFile(path.join(directory,name),bytes);
   fixtures.push({name,density:item.density,theme:item.theme,viewport:item.viewport,box:item.box,sha256:createHash('sha256').update(bytes).digest('hex')});
  }
  await fs.writeFile(path.join(directory,'reference.json'),JSON.stringify({browser:report.browser,
   source:'web/src/style.css',sourceSha256:createHash('sha256').update(css).digest('hex'),
   command:'sh android/tools/run.sh node android/tools/probe-css-paint.mjs --write-border-fixtures',
   purpose:'Unmodified full-viewport actual CSS screenshots, without shadows, at three densities and both themes. No cropping, rescaling, or native-derived pixels.',fixtures},null,2)+'\n');
 }
 console.log(JSON.stringify({browser:report.browser,cases:cases.map(item=>({density:item.density,theme:item.theme,shadow:item.shadow,commands:item.snapshots.flatMap(snapshot=>snapshot.commands)}))},null,2));
} finally {await browser?.close();}
