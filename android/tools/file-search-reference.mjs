import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

// Actual FileSidebarHeader and Chromium's UA search control, never a styled mirror.
export async function renderFileSearchReferences({browser,url,output,fontCSS,probe=false}) {
 const cases=[];
 for(const density of probe?[2.625]:[1,2.625,3])for(const locale of probe?['zh-CN']:['en','zh-CN','ja'])for(const theme of probe?['light']:['light','dark'])for(const width of probe?[360]:[220,360,412]) {
  const context=await browser.newContext({viewport:{width,height:800},deviceScaleFactor:density,isMobile:true,hasTouch:true});
  try {
   const names=['plain.txt',locale==='ja'?'共有項目.txt':'共享项目.txt'];
   await context.route('**/api/projects/font-*/files?*',route=>route.fulfill({json:{path:'.',entries:names.map(name=>({name,path:name,kind:'text'}))}}));
   const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)));
   await page.goto(`${url}?screen=files-fonts&locale=${locale}&theme=${theme}`);
   await page.waitForFunction(()=>document.querySelector('file-tree-container')?.shadowRoot?.querySelectorAll('[data-type="item"]').length===2);
   await page.evaluate(({locale,theme})=>{document.documentElement.lang=locale;document.documentElement.dataset.theme=theme;},{locale,theme});
   await page.addStyleTag({content:fontCSS+`file-tree-container{--trees-font-family-override:AndroidRoboto,${locale==='ja'?'AndroidCjkJP':'AndroidCjkSC'},sans-serif}`});await page.evaluate(()=>document.fonts.ready);
   const client=await context.newCDPSession(page);await client.send('DOM.enable');let layers=[];
   if(probe) {client.on('LayerTree.layerTreeDidChange',event=>{if(event.layers)layers=event.layers;});await client.send('LayerTree.enable');}
   const records=[];
   async function capture(id) {
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    const geometry=await page.evaluate(()=>{
     const rect=element=>{if(!element)return null;const r=element.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};
     const search=document.querySelector('.file-sidebar-search'),input=search?.querySelector('input'),s=search&&getComputedStyle(search),i=input&&getComputedStyle(input);
     const rgba=value=>{const c=document.createElement('canvas').getContext('2d',{willReadFrequently:true});c.fillStyle=value;c.fillRect(0,0,1,1);return [...c.getImageData(0,0,1,1).data];};
     return {panel:rect(document.querySelector('.file-sidebar')),header:rect(document.querySelector('.file-sidebar-header')),
      search:rect(search),input:rect(input),focused:!!input&&document.activeElement===input,value:input?.value??'',hint:input?.placeholder??'',
      style:s?{radius:parseFloat(s.borderRadius),border:parseFloat(s.borderWidth),borderColor:rgba(s.borderColor),fill:rgba(s.backgroundColor),
       outline:parseFloat(s.outlineWidth),outlineStyle:s.outlineStyle,offset:parseFloat(s.outlineOffset),outlineColor:rgba(s.outlineColor),fontSize:parseFloat(i.fontSize),
       textColor:rgba(i.color),hintColor:rgba(getComputedStyle(input,'::placeholder').color),paddingLeft:parseFloat(i.paddingLeft),paddingRight:parseFloat(i.paddingRight)}:null,
      actions:[...document.querySelectorAll('.file-sidebar-actions button')].map(button=>({box:rect(button),icon:rect(button.querySelector('svg')),label:button.getAttribute('aria-label'),expanded:button.getAttribute('aria-expanded')}))};
    });
    const ua=[];
    if(geometry.input) {
     const {root}=await client.send('DOM.getDocument',{depth:-1,pierce:true});
     const find=node=>node.nodeName==='INPUT'&&node.attributes?.some((value,index)=>index%2===0&&value==='type'&&node.attributes[index+1]==='search')?node:[...node.children||[],...node.shadowRoots||[]].map(find).find(Boolean);
     const input=find(root);if(!input)throw new Error('Missing actual UA search input');
     async function read(node) {
      if(node.nodeType===1||node.nodeType===3&&node.nodeValue) {
       const {object}=await client.send('DOM.resolveNode',{backendNodeId:node.backendNodeId});
       try {
        const {result,exceptionDetails}=await client.send('Runtime.callFunctionOn',{objectId:object.objectId,returnByValue:true,functionDeclaration:`function(){
         const element=this.nodeType===1?this:this.parentElement,style=getComputedStyle(element),range=document.createRange();range.selectNodeContents(this);
         const r=this.nodeType===1?this.getBoundingClientRect():range.getBoundingClientRect();
         return {text:this.nodeType===3?this.textContent:null,x:r.x,y:r.y,width:r.width,height:r.height,display:style.display,visibility:style.visibility,
          opacity:parseFloat(style.opacity),pointerEvents:style.pointerEvents,font:style.font,color:style.color,appearance:style.appearance,
          backgroundImage:style.backgroundImage,maskImage:style.maskImage,marginLeft:style.marginLeft,marginRight:style.marginRight};
        }`});
        if(exceptionDetails)throw new Error(JSON.stringify(exceptionDetails));
        ua.push({node:node.nodeName,attributes:node.attributes,...result.value});
       } finally {await client.send('Runtime.releaseObject',{objectId:object.objectId});}
      }
      for(const child of [...node.children||[],...node.shadowRoots||[]])await read(child);
     }
     for(const shadow of input.shadowRoots||[])await read(shadow);
    }
    records.push({id,...geometry,ua});
    if(id==='typed'||id==='blurred')await page.screenshot({path:path.join(output,`web-file-search-${locale}-${theme}-${width}-dpr${density}-${id}.png`)});
    if(probe&&id==='typed') {
     let paints;
     for(let attempt=0;attempt<3;attempt++) {
      await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.screenshot();
      paints=[];
      await fs.writeFile(path.join(output,'web-file-search-layers-probe.json'),JSON.stringify(layers,null,2)+'\n');
      try {
       // DOM-backed pictures include the control; platform caret layers have no picture snapshot.
       for(const layer of layers.filter(layer=>layer.backendNodeId&&layer.drawsContent&&layer.paintCount>0&&layer.width>0&&layer.height>0)) {
        const reasons=await client.send('LayerTree.compositingReasons',{layerId:layer.layerId});
        if(reasons.compositingReasonIds?.includes('Caret')) {paints.push({layer,reasons,unavailable:'Caret layer has no picture snapshot'});continue;}
        const {snapshotId}=await client.send('LayerTree.makeSnapshot',{layerId:layer.layerId});
        try {const {commandLog}=await client.send('LayerTree.snapshotCommandLog',{snapshotId});paints.push({layer,commands:commandLog});}
        finally {await client.send('LayerTree.releaseSnapshot',{snapshotId});}
       }
       if(!paints.length)throw new Error('No painted search-control layers');
       break;
      } catch(error) {if(attempt===2||!String(error).includes('No layer matching given id'))throw error;}
     }
     await fs.writeFile(path.join(output,'web-file-search-paint-probe.json'),JSON.stringify(paints,null,2)+'\n');
    }
   }
   const toggle=page.locator('.file-sidebar-actions button').first();
   await capture('closed');await toggle.click();await capture('open-empty');
   await page.locator('.file-sidebar-search input').fill(locale==='en'?'plain':locale==='ja'?'共有':'共享');await capture('typed');
   await page.locator('.file-sidebar-actions button').nth(1).focus();await capture('blurred');
   await page.locator('.file-sidebar-search input').focus();await capture('refocused');
   const cancel=records.at(-1).ua.find(node=>node.attributes?.includes('-webkit-search-cancel-button'));
   if(!cancel||cancel.width<=0||cancel.height<=0)throw new Error('Missing actual UA cancel geometry: '+JSON.stringify(records.at(-1).ua));
   await page.mouse.click(cancel.x+cancel.width/2,cancel.y+cancel.height/2);await capture('cleared');
   if(records.at(-1).value!=='')throw new Error('Actual UA clear did not clear the controlled search value');
   await page.locator('.file-sidebar-search input').press('Escape');await capture('escaped');
   await toggle.click();await capture('reopened');await toggle.click();await capture('closed-again');
   if(errors.length)throw new Error(errors.join('\n'));
   cases.push({density,locale,theme,width,names,records});
  } finally {await context.close();}
 }
 const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
 const contents=JSON.stringify({browser:browser.version(),purpose:'Actual FileSidebarHeader geometry/styles and UA search clear/focus transitions; not native/device raster parity',cases},null,2)+'\n';
 await fs.writeFile(path.join(output,probe?'web-file-search-probe.json':'web-file-search.json'),contents);
 if(!probe)await fs.writeFile(path.join(root,'android/app/src/test/resources/web-file-search.json'),contents);
 console.log(`Recorded ${cases.length} actual FileSidebar search scenes with ${cases[0].records.length} transitions each.`);
}
