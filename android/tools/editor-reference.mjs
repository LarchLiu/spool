import fs from 'node:fs/promises';
import path from 'node:path';
import {editorCases} from './ui-fixtures.mjs';

// CDP includes the textarea's actual UA-shadow text boxes, not a separately styled mirror.
export async function renderEditorReferences({browser,url,output,fontCSS}) {
 const samples=[];
 for(const width of [320,360,412,900]) {
  const context=await browser.newContext({viewport:{width,height:800},deviceScaleFactor:1,isMobile:true,hasTouch:true});
  try {
   const page=await context.newPage();
   await page.goto(`${url}?screen=workspace&theme=light`);await page.waitForSelector('.composer textarea');
   await page.addStyleTag({content:fontCSS});await page.evaluate(()=>document.fonts.ready);
   await page.evaluate(()=>{for(const animation of document.getAnimations())if(Number.isFinite(animation.effect.getTiming().iterations))animation.finish();});
   const session=await context.newCDPSession(page);
   try {
    await session.send('DOM.enable'); await session.send('CSS.enable');
    for(const {id,value,locale} of editorCases) {
     await page.evaluate(async ({value,locale})=>{
      const {resizeTextarea}=await import('/src/autosize-textarea.tsx');
      const {translator}=await import('/src/i18n.ts');
      const editor=document.querySelector('.composer textarea');document.documentElement.lang=locale;
      editor.placeholder=translator(locale)('Message {agent}',{agent:'Codex'});editor.value=value;
      await document.fonts.load(getComputedStyle(editor).font, value||editor.placeholder);await document.fonts.ready;
      resizeTextarea(editor);
     },{value,locale});
     const editor=await page.locator('.composer textarea').evaluate(element=>{
      const r=element.getBoundingClientRect(),style=getComputedStyle(element),placeholder=getComputedStyle(element,'::placeholder');
      const canvas=document.createElement('canvas'),c=canvas.getContext('2d');c.font=style.font;
      const metrics=c.measureText(element.value||element.placeholder);
      return {x:r.x,y:r.y,width:r.width,height:r.height,font:style.font,weight:style.fontWeight,lineHeight:parseFloat(style.lineHeight),
       paddingTop:parseFloat(style.paddingTop),paddingBottom:parseFloat(style.paddingBottom),paddingLeft:parseFloat(style.paddingLeft),
       placeholderFont:placeholder.font,placeholderWeight:placeholder.fontWeight,scrollTop:element.scrollTop,scrollHeight:element.scrollHeight,
       ascent:metrics.fontBoundingBoxAscent,descent:metrics.fontBoundingBoxDescent};
     });
     const {root}=await session.send('DOM.getDocument',{depth:-1,pierce:true}),texts=[];
     function find(node) {
      if(node.nodeName==='TEXTAREA')return node;
      for(const child of [...node.children||[],...node.shadowRoots||[]]) {const result=find(child);if(result)return result;}
     }
     async function read(node) {
      if(node.nodeType===3&&node.nodeValue) {
       const {object}=await session.send('DOM.resolveNode',{backendNodeId:node.backendNodeId});
       const {result,exceptionDetails}=await session.send('Runtime.callFunctionOn',{objectId:object.objectId,returnByValue:true,
        functionDeclaration:`function(){
         const style=getComputedStyle(this.parentElement),range=document.createRange();range.selectNodeContents(this);
         const rects=[...range.getClientRects()].map(r=>({x:r.x,y:r.y,width:r.width,height:r.height}));
         const chars=Array.from({length:this.length},(_,i)=>{range.setStart(this,i);range.setEnd(this,i+1);const r=range.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};});
         return {text:this.textContent,font:style.font,weight:style.fontWeight,visible:style.display!=='none'&&style.visibility!=='hidden',rects,chars};
        }`});
       if(exceptionDetails)throw new Error(JSON.stringify(exceptionDetails));
       const fonts=await session.send('CSS.getPlatformFontsForNode',{nodeId:node.parentId});
       texts.push({...result.value,fonts:fonts.fonts});await session.send('Runtime.releaseObject',{objectId:object.objectId});
      }
      for(const child of [...node.children||[],...node.shadowRoots||[]])await read(child);
     }
     await read(find(root));
     samples.push({width,id,value,locale,editor,texts});
     await page.screenshot({path:path.join(output,`web-editor-${width}-${id}.png`)});
    }
   } finally {await session.detach();}
  } finally {await context.close();}
 }
 await fs.writeFile(path.join(output,'web-editor-text.json'),JSON.stringify(samples,null,2)+'\n');
 console.log('Recorded actual textarea UA-shadow text boxes and font styles at phone/wide widths.');
}
