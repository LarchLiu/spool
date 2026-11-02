import fs from 'node:fs/promises';
import path from 'node:path';
import {commandCases} from './ui-fixtures.mjs';

export async function renderCommandReferences({browser,url,output,fontCSS}) {
 const fontSamples=[];
 for(const width of [320,360,412])for(const theme of ['light','dark'])for(const locale of ['en','zh-CN','ja']) {
  const context=await browser.newContext({viewport:{width,height:800},deviceScaleFactor:1,isMobile:true,hasTouch:true});
  try {
   const page=await context.newPage(),samples=[],errors=[];page.on('pageerror',error=>errors.push(String(error)));
   for(const item of commandCases) {
    await page.goto(`${url}?screen=commands&state=${item.id}&locale=${locale}`);
    await page.waitForSelector('.command-scene .composer');
    await page.evaluate(({theme,locale})=>{document.documentElement.dataset.theme=theme;document.documentElement.lang=locale;},{theme,locale});
    await page.addStyleTag({content:fontCSS+'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}.goal-command-notice pre,.slash-command-menu kbd{font-family:AndroidMono,AndroidCjkSC,monospace}.goal-command-notice pre:lang(ja),.slash-command-menu kbd:lang(ja){font-family:AndroidMono,AndroidCjkJP,monospace}'});
    await page.evaluate(()=>document.fonts.ready);
    const session=await context.newCDPSession(page);
    try {
     await session.send('DOM.enable');await session.send('CSS.enable');
     const {root}=await session.send('DOM.getDocument');
     for(const selector of ['.slash-command-menu strong','.slash-command-menu small','.slash-command-menu kbd','.goal-command-hint','.goal-command-notice pre','.error']) {
      const {nodeId}=await session.send('DOM.querySelector',{nodeId:root.nodeId,selector});
      if(!nodeId)continue;
      const {fonts}=await session.send('CSS.getPlatformFontsForNode',{nodeId});
      if(fonts.some(font=>font.glyphCount>0&&!font.isCustomFont))throw new Error(`Uncontrolled host font in ${locale}/${item.id}/${selector}: ${JSON.stringify(fonts)}`);
      fontSamples.push({width,theme,locale,id:item.id,selector,fonts});
     }
    } finally {await session.detach();}
    const geometry=await page.evaluate(()=>{
     const box=element=>{const r=element.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};
     const read=selector=>box(document.querySelector(selector));
     const text=selector=>{const element=document.querySelector(selector),rgb=getComputedStyle(element).color.match(/[\d.]+/g).map(Number);return{...box(element),text:element.textContent,color:(0xff000000|rgb[0]<<16|rgb[1]<<8|rgb[2])|0};};
     const result={composer:read('.composer-wrap'),surface:read('.composer'),input:read('textarea'),bottom:read('.composer-bottom')};
     if(document.querySelector('.slash-command-menu'))Object.assign(result,{menu:read('.slash-command-menu'),option:read('.slash-command-menu button'),symbol:read('.slash-command-menu svg'),title:text('.slash-command-menu strong'),detail:text('.slash-command-menu small'),shortcut:text('.slash-command-menu kbd')});
     if(document.querySelector('.goal-command-hint'))result.hint=text('.goal-command-hint');
     if(document.querySelector('.goal-command-notice'))Object.assign(result,{notice:read('.goal-command-notice'),noticeText:text('.goal-command-notice pre'),close:read('.goal-command-notice .icon')});
     if(document.querySelector('.error'))result.error=text('.error');
     return result;
    });
    samples.push({id:item.id,...geometry});
    await page.screenshot({path:path.join(output,`web-commands-${width}-${theme}-${locale}-${item.id}.png`)});
   }
   if(errors.length)throw new Error(errors.join('\n'));
   await fs.writeFile(path.join(output,`web-commands-${width}-${theme}-${locale}.json`),JSON.stringify(samples,null,2)+'\n');
  } finally {await context.close();}
 }
 await fs.writeFile(path.join(output,'web-command-fonts.json'),JSON.stringify(fontSamples,null,2)+'\n');
 console.log('Rendered actual SlashCommandMenu plus original composer hint/result/error CSS in all three locales.');
}
