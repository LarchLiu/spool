import fs from 'node:fs/promises';
import path from 'node:path';

// Uses the real Sidebar, FileSidebar and AutosizeTextarea mounted by the reference app.
export async function renderViewportReferences({browser,url,output,fontCSS}) {
 const viewports=[{id:'full',width:360,height:800},{id:'keyboard',width:360,height:560},{id:'split',width:280,height:360},{id:'narrow',width:220,height:500},{id:'wide',width:900,height:500}];
 const settle=page=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 const select=async(page,side)=>{
  await page.evaluate(side=>{
   document.querySelector('.app > .sidebar').classList.toggle('open',side==='left');
   document.querySelector('.app > .file-sidebar').classList.toggle('open',side==='right');
   for(const pane of document.querySelectorAll('.app > .sidebar,.app > .file-sidebar'))for(const animation of pane.getAnimations())animation.finish();
  },side);
  await settle(page);
 };
 for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width:360,height:800},deviceScaleFactor:1,isMobile:true,hasTouch:true});
  try {
   const page=await context.newPage(),errors=[];
   page.on('pageerror',error=>errors.push(String(error)));
   await page.goto(`${url}?screen=viewport&theme=${theme}`);
   await page.waitForSelector('.composer textarea');
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   await page.addStyleTag({content:fontCSS}); await page.evaluate(()=>document.fonts.ready);
   await page.evaluate(()=>{
    const transcript=document.createElement('div');transcript.style.height='3000px';
    document.querySelector('.history-inner').replaceChildren(transcript);
   });
   await settle(page);
   for(const viewport of viewports) {
    await page.setViewportSize({width:viewport.width,height:viewport.height}); await settle(page);
    for(const side of ['workspace','left','right']) {
     await select(page,side);
     const geometry=await page.evaluate(side=>{
      const box=element=>{const r=element.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};
      const result={page:box(document.querySelector('.workspace')),header:box(document.querySelector('.workspace > header')),history:box(document.querySelector('.history')),composer:box(document.querySelector('.composer-wrap')),surface:box(document.querySelector('.composer')),input:box(document.querySelector('.composer textarea')),model:box(document.querySelector('.picker-summary')),send:box(document.querySelector('.composer .send')),permissionLabelVisible:getComputedStyle(document.querySelector('.permission-summary > span')).display!=='none'};
      if(side!=='workspace')Object.assign(result,{sheet:box(document.querySelector(side==='left'?'.app > .sidebar':'.app > .file-sidebar')),noBackdrop:!document.querySelector('.scrim')});
      return result;
     },side);
     const name=`viewport-${viewport.id}-${side}-${theme}`;
     await fs.writeFile(path.join(output,`web-${name}.json`),JSON.stringify(geometry,null,2)+'\n');
     await page.screenshot({path:path.join(output,`web-${name}.png`)});
    }
   }
   if(errors.length)throw new Error(errors.join('\n'));
   if(theme==='light') {
    const motion={};
    for(const side of ['left','right']) {
     await page.setViewportSize({width:360,height:800});await select(page,'workspace');
     const selector=side==='left'?'.app > .sidebar':'.app > .file-sidebar';
     const sample=()=>page.locator(selector).evaluate(pane=>{const r=pane.getBoundingClientRect();return{width:r.width,translation:new DOMMatrixReadOnly(getComputedStyle(pane).transform).m41};});
     const begin=async(open)=>page.locator(selector).evaluate((pane,open)=>{
      pane.classList.toggle('open',open);getComputedStyle(pane).transform;
      const animation=pane.getAnimations()[0];if(!animation)throw new Error('Missing original CSS drawer transition');
      animation.pause();animation.currentTime=80;
     },open);
     await begin(true); const before=await sample();
     await page.setViewportSize({width:220,height:500});await settle(page);const resized=await sample();
     await page.locator(selector).evaluate(pane=>{for(const animation of pane.getAnimations())animation.finish();});
     await begin(false);const closingBefore=await sample();
     await page.setViewportSize({width:412,height:800});await settle(page);const closingResized=await sample();
     motion[side]={opening:{before,resized},closing:{before:closingBefore,resized:closingResized}};
     await page.locator(selector).evaluate(pane=>{for(const animation of pane.getAnimations())animation.finish();});
    }
    await fs.writeFile(path.join(output,'web-viewport-motion.json'),JSON.stringify(motion,null,2)+'\n');
   }
  } finally {await context.close();}
 }
 console.log('Rendered live resized workspace/drawer references and original CSS transition samples.');
}
