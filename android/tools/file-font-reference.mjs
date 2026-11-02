import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {fileFocusReference} from './file-focus-reference.mjs';

export const fileFontFixtures=[{locale:'zh-CN',name:'共享项目'},{locale:'ja',name:'共有項目'}];

export async function renderFileFontReferences({browser,url,output,fontCSS}) {
 const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
 const cases=[],focusCases=[];
 for(const density of [1,2.625,3])for(const fixture of fileFontFixtures)for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width:360,height:800},deviceScaleFactor:density,isMobile:true,hasTouch:true});
  try {
   const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)));
   await page.goto(`${url}?screen=files-fonts&locale=${fixture.locale}&theme=${theme}`);
   await page.waitForFunction(()=>document.querySelector('file-tree-container')?.shadowRoot?.querySelector('[data-type="item"]'));
   await page.evaluate(({locale,theme})=>{document.documentElement.lang=locale;document.documentElement.dataset.theme=theme;},{locale:fixture.locale,theme});
   const family=fixture.locale==='ja'?'AndroidCjkJP':'AndroidCjkSC';
   await page.addStyleTag({content:fontCSS+`file-tree-container{--trees-font-family-override:AndroidRoboto,${family},sans-serif}`});
   await page.evaluate(()=>document.fonts.ready);
   const client=await context.newCDPSession(page);await client.send('DOM.enable');await client.send('CSS.enable');
   focusCases.push({...fixture,density,theme,records:await fileFocusReference(page,fixture.name)});
   const records=[];let noMatchPaths;
   for(const [index,match] of [false,true,false].entries()) {
    if(index===1) {
     await page.locator('.file-sidebar-actions button').first().click();
     await page.locator('.file-sidebar-search input').fill('__spool_font_probe_no_match__');
     await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
     noMatchPaths=await page.locator('file-tree-container').evaluate(host=>[...host.shadowRoot.querySelectorAll('[data-type="item"]')].map(row=>row.getAttribute('data-item-path')));
     if(noMatchPaths.length!==2)throw new Error('Actual Pierre no-match behavior changed; keep the native search oracle explicit');
     await page.locator('.file-sidebar-search input').fill(fixture.name);
     await page.waitForFunction(()=>document.querySelector('file-tree-container')?.shadowRoot?.querySelectorAll('[data-type="item"]').length===1);
    } else if(index===2)await page.locator('.file-sidebar-actions button').first().click();
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    const record=await page.locator('file-tree-container').evaluate((host,name)=>{
     const rows=[...host.shadowRoot.querySelectorAll('[data-type="item"]')];
     const row=rows.find(item=>item.getAttribute('data-item-path')===name),content=row.querySelector('[data-item-section="content"]');
     const visible=[...content.querySelectorAll('[data-truncate-content="visible"]')];
     if(!visible.length)throw new Error('No actual visible filename nodes');
     const walker=document.createTreeWalker(visible[0],NodeFilter.SHOW_TEXT),node=walker.nextNode()?.parentElement;
     if(!node)throw new Error('No filename text node');
     node.setAttribute('data-font-probe','filename');
     const s=getComputedStyle(node),r=row.getBoundingClientRect();
     return {text:visible.map(element=>element.textContent).join(''),weight:s.fontWeight,size:parseFloat(s.fontSize),style:s.fontStyle,synthesis:s.fontSynthesis,color:s.color,matchAttribute:row.getAttribute('data-item-search-match'),rowCount:rows.length,row:{x:r.x,y:r.y,width:r.width,height:r.height}};
    },fixture.name);
    const {root:doc}=await client.send('DOM.getDocument',{depth:-1,pierce:true});
    const find=node=>node.nodeName==='FILE-TREE-CONTAINER'?node:[...(node.children||[]),...(node.shadowRoots||[])].map(find).find(Boolean);
    const host=find(doc);if(!host?.shadowRoots?.length)throw new Error('No actual FileTree shadow root');
    const {nodeId}=await client.send('DOM.querySelector',{nodeId:host.shadowRoots[0].nodeId,selector:'[data-font-probe="filename"]'});
    const {fonts}=await client.send('CSS.getPlatformFontsForNode',{nodeId});
    if(record.text!==fixture.name||!['400','600'].includes(record.weight)||record.synthesis!=='none'||!fonts.length||fonts.some(font=>!font.isCustomFont))throw new Error('Uncontrolled actual file-row font: '+JSON.stringify({record,fonts}));
    records.push({match,...record,fonts});
    if(match) {
     const stem=`web-file-font-${fixture.locale}-${theme}-dpr${density}`;
     await page.screenshot({path:path.join(output,stem+'.png')});
    }
   }
   if(errors.length)throw new Error(errors.join('\n'));
   cases.push({...fixture,density,theme,noMatchPaths,records});
  } finally {await context.close();}
 }
 const report={browser:browser.version(),purpose:'Actual FileSidebar/FileTree visible filename fonts during real search, not a forced CSS-attribute control or native/device raster pass',cases};
 await fs.writeFile(path.join(output,'web-file-fonts.json'),JSON.stringify(report,null,2)+'\n');
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-file-fonts.json'),JSON.stringify(report,null,2)+'\n');
 const focusReport={browser:browser.version(),purpose:'Actual FileSidebar external-search and tree focus transitions, not a full raster/device pass',cases:focusCases};
 await fs.writeFile(path.join(output,'web-file-focus.json'),JSON.stringify(focusReport,null,2)+'\n');
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-file-focus.json'),JSON.stringify(focusReport,null,2)+'\n');
 console.log(`Measured ${cases.length} actual FileSidebar font scenes, normal/search/reuse, both locales/themes and three densities.`);
}
