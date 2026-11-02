import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const names=['NativeWorkspaceConfiguration.android.test.ts','.gitignore','filename.','file.abcdefghijk',
 'prefix part with spaces and suffix.md','ab cd ef ghi jkl','a.js','a','a  b','e\u0301','spaced  共享   file.txt',
 '共享项目配置文件和详细说明文档.txt','共有項目設定と詳細な説明文書.md','notes-😀-extra-long-filename.txt'];

export async function renderFileOverflowReferences({browser,url,output,fontCSS}) {
 const cases=[];let markerTiming;
 for(const density of [1,2.625,3])for(const locale of ['en','zh-CN','ja'])for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width:360,height:800},deviceScaleFactor:density,isMobile:true,hasTouch:true});
  try {
   await context.route('**/api/projects/font-*/files?*',route=>route.fulfill({json:{path:'.',entries:names.map(name=>({name,path:name,kind:'text'}))}}));
   const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)));
   await page.goto(`${url}?screen=files-fonts&locale=${locale}&theme=${theme}`);
   await page.waitForFunction(count=>document.querySelector('file-tree-container')?.shadowRoot?.querySelectorAll('[data-type="item"]').length===count,names.length);
   await page.evaluate(({locale,theme})=>{document.documentElement.lang=locale;document.documentElement.dataset.theme=theme;},{locale,theme});
   await page.addStyleTag({content:fontCSS+`file-tree-container{--trees-font-family-override:AndroidRoboto,${locale==='ja'?'AndroidCjkJP':'AndroidCjkSC'},sans-serif}`});
   await page.evaluate(()=>document.fonts.ready);
   const client=await context.newCDPSession(page);let layers=[];
   client.on('LayerTree.layerTreeDidChange',event=>{if(event.layers)layers=event.layers;});
   await client.send('LayerTree.enable');
   const scenes=[{id:'normal',width:360},{id:'narrow',width:220},{id:'wide',width:412},
    ...[0,3,10,20,40].map(width=>({id:'constrained-'+width,width:360,limit:width}))];
   for(const scene of scenes) {
    await page.setViewportSize({width:scene.width,height:800});
    await page.locator('file-tree-container').evaluate((host,limit)=>{
     for(const content of host.shadowRoot.querySelectorAll('[data-item-section="content"]')) content.style.maxWidth=limit===null?'':limit+'px';
    },scene.limit??null);
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    if(density===1&&locale==='en'&&theme==='light'&&scene.id==='narrow')markerTiming=await page.locator('file-tree-container').evaluate(host=>{
     const marker=[...host.shadowRoot.querySelectorAll('[data-truncate-marker]')].find(marker=>marker.getAnimations().some(animation=>animation.transitionProperty==='opacity'));
     if(!marker)throw new Error('No actual marker opacity transition');
     const animation=marker.getAnimations().find(animation=>animation.transitionProperty==='opacity');animation.pause();
     const samples=[0,25,50,75,100].map(time=>{animation.currentTime=time;return{time,opacity:parseFloat(getComputedStyle(marker).opacity)};});
     return {timing:animation.effect.getTiming(),samples};
    });
    await page.locator('file-tree-container').evaluate(host=>{
     for(const marker of host.shadowRoot.querySelectorAll('[data-truncate-marker]'))for(const animation of marker.getAnimations())animation.finish();
    });
    const rows=await page.locator('file-tree-container').evaluate((host,limit)=>{
     const rect=element=>{const r=element.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};
     return [...host.shadowRoot.querySelectorAll('[data-type="item"]')].map(row=>{
      const content=row.querySelector('[data-item-section="content"]'),box=rect(content),r=row.getBoundingClientRect(),s=getComputedStyle(row);
      const available=Math.max(0,Math.min(limit??Infinity,r.right-parseFloat(s.paddingRight)-box.x));
      const segments=[...content.querySelectorAll('[data-truncate-container]')].map(segment=>{
       const visible=segment.querySelector('[data-truncate-content="visible"]'),range=document.createRange();range.selectNodeContents(visible);
       const ink=range.getBoundingClientRect(),marker=segment.querySelector('[data-truncate-marker]'),style=getComputedStyle(marker);
       return {text:visible.textContent,mode:segment.getAttribute('data-truncate-container'),box:rect(segment),ink:{x:ink.x,width:ink.width},
        marker:{...rect(marker),text:marker.textContent,opacity:parseFloat(style.opacity),color:style.color}};
      });
      if(segments.map(segment=>segment.text).join('')!==row.getAttribute('data-item-path'))throw new Error('Visible filename source differs');
      return {name:row.getAttribute('data-item-path'),available,row:rect(row),icon:rect(row.querySelector('[data-item-section="icon"] svg')),content:box,segments};
     });
    },scene.limit??null);
    if(scene.limit===undefined) {
     await page.screenshot();const baselines=[],paintLog=[];
     if(density===2.625&&locale==='en')await fs.writeFile(path.join(output,'web-file-row-layers-'+scene.id+'.json'),JSON.stringify(layers,null,2)+'\n');
     const layerCoordinates=layer=>{
      const matrix=layer.transform;
      if(matrix&&(matrix[0]!==matrix[5]||matrix[0]<=0||matrix.some((value,index)=>![0,5,12,13].includes(index)&&value!==([10,15].includes(index)?1:0))))throw new Error('Unsupported nonuniform/non-affine file-row layer: '+JSON.stringify(layer));
      const parent=layers.find(candidate=>candidate.layerId===layer.parentLayerId);
      const p=parent?layerCoordinates(parent):{scale:1,y:0};
      return {scale:p.scale*(matrix?.[5]||1),y:p.y+p.scale*(layer.offsetY+(matrix?.[13]||0))};
     };
     // LayerTree initially reports CSS units, but viewport changes can report device units.
     // The actual viewport layers determine the coordinate system, not filename baselines.
     const viewportScales=[...new Set(layers.filter(layer=>layer.width===scene.width&&layer.height===800).map(layer=>layerCoordinates(layer).scale))];
     if(viewportScales.length!==1||![1,density].includes(viewportScales[0]))throw new Error('Ambiguous actual viewport-layer coordinate units');
     const coordinateScale=viewportScales[0];
     for(const layer of layers.filter(layer=>layer.drawsContent&&layer.width>0&&layer.height>0)) {
      const {snapshotId}=await client.send('LayerTree.makeSnapshot',{layerId:layer.layerId});
      try {
       const {commandLog}=await client.send('LayerTree.snapshotCommandLog',{snapshotId});
       paintLog.push({layer,commands:commandLog.filter(command=>!['drawPath','drawRRect','drawRect','drawImageRect'].includes(command.method))});
       const coordinates=layerCoordinates(layer);
       for(const command of commandLog)if(command.method==='drawTextBlob')baselines.push((command.params.y*coordinates.scale+coordinates.y)/coordinateScale);
      } finally {await client.send('LayerTree.releaseSnapshot',{snapshotId});}
     }
     for(const row of rows)row.baselines=[...new Set(baselines.filter(y=>y>=row.row.y&&y<row.row.y+row.row.height))].sort((a,b)=>a-b);
     if(density===1&&locale==='en'&&theme==='light'&&scene.id==='normal')await fs.writeFile(path.join(output,'web-file-row-paint.json'),JSON.stringify(paintLog,null,2)+'\n');
     if(rows.some(row=>row.baselines.length!==1))throw new Error('Expected exactly one actual Web filename paint baseline per row: '+JSON.stringify(rows.map(row=>[row.name,row.row,row.baselines])));
    }
    cases.push({density,locale,theme,...scene,rows});
    if(density===2.625&&locale==='zh-CN'&&scene.id==='narrow')await page.screenshot({path:path.join(output,`web-file-overflow-${theme}.png`)});
   }
   if(errors.length)throw new Error(errors.join('\n'));
  } finally {await context.close();}
 }
 const report={browser:browser.version(),purpose:'Actual FileSidebar/Pierre MiddleTruncate with original CSS and pinned fonts; normal mobile widths plus explicitly isolated constrained-content controls; finite marker animations finished for steady-state capture, not native/device raster parity',markerTiming,cases};
 const contents=JSON.stringify(report,null,2)+'\n',root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
 await fs.writeFile(path.join(output,'web-file-overflow.json'),contents);
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-file-overflow.json'),contents);
 console.log(`Recorded ${cases.length} actual file-overflow scenes (${names.length} filenames each), including separate narrow-content controls.`);
}
