import fs from 'node:fs/promises';
import path from 'node:path';
import {composerLayoutCases} from './ui-fixtures.mjs';

// Measure actual CSS and AutosizeTextarea in the composed workspace, in physical pixels.
export async function renderComposerLayoutReferences({browser,url,output,fontCSS}) {
 const samples=[],density=2.625;
 for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width:360,height:800},deviceScaleFactor:density,isMobile:true,hasTouch:true});
  try {
   const page=await context.newPage(),errors=[];
   page.on('pageerror',error=>errors.push(String(error)));
   await page.goto(`${url}?screen=viewport&theme=${theme}`);
   await page.waitForSelector('.composer textarea');
   await page.addStyleTag({content:fontCSS});
   await page.evaluate(theme=>{
    document.documentElement.dataset.theme=theme;
    const transcript=document.createElement('div');transcript.style.height='3000px';
    document.querySelector('.history-inner').replaceChildren(transcript);
    for(const animation of document.getAnimations())if(Number.isFinite(animation.effect.getTiming().iterations))animation.finish();
   },theme);
   for(const sample of composerLayoutCases) {
    await page.setViewportSize({width:sample.width,height:sample.height});
    await page.evaluate(async sample=>{
     const {resizeTextarea}=await import('/src/autosize-textarea.tsx');
     const {translator}=await import('/src/i18n.ts');
     const t=translator(sample.locale),input=document.querySelector('.composer textarea');
     document.documentElement.lang=sample.locale;
     input.placeholder=t('Message {agent}',{agent:'Codex'});input.value=sample.value;
     document.querySelector('.workspace > header .chat-heading strong').textContent=t('New chat');
     document.querySelector('.permission-summary > span').textContent=t('Ask for approval');
     await document.fonts.load(getComputedStyle(input).font,sample.value||input.placeholder);
     await document.fonts.ready;resizeTextarea(input);
     await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
    },sample);
    const geometry=await page.evaluate(density=>{
     const rect=element=>{const r=element.getBoundingClientRect();return{x:r.x*density,y:r.y*density,width:r.width*density,height:r.height*density};};
     const composer=document.querySelector('.composer'),input=composer.querySelector('textarea'),r=composer.getBoundingClientRect(),style=getComputedStyle(composer);
     const model=composer.querySelector('.picker-summary'),permission=composer.querySelector('.permission-summary');
     const modelLabel=model.querySelector('span'),range=document.createRange();range.selectNodeContents(modelLabel);
     const inputStyle=getComputedStyle(input);
     return {page:rect(document.querySelector('.workspace')),header:rect(document.querySelector('.workspace > header')),history:rect(document.querySelector('.history')),
      composer:rect(document.querySelector('.composer-wrap')),surface:rect(composer),input:rect(input),controls:rect(composer.querySelector('.composer-bottom')),
      add:rect(composer.querySelector('.attachment-add')),permission:rect(permission),model:rect(model),send:rect(composer.querySelector('.send')),
      paint:{left:Math.round(r.left)*density,top:Math.round(r.top)*density,right:Math.round(r.right)*density,bottom:Math.round(r.bottom)*density,radius:parseFloat(style.borderRadius)*density},
      inputStyle:{paddingLeft:parseFloat(inputStyle.paddingLeft)*density,paddingRight:parseFloat(inputStyle.paddingRight)*density,paddingTop:parseFloat(inputStyle.paddingTop)*density,paddingBottom:parseFloat(inputStyle.paddingBottom)*density,fontSize:parseFloat(inputStyle.fontSize)*density,lineHeight:parseFloat(inputStyle.lineHeight)*density},
      value:input.value,hint:input.placeholder,modelLabel:modelLabel.textContent,permissionLabel:permission.querySelector('span').textContent,
      modelLabelTruncated:range.getBoundingClientRect().width>modelLabel.getBoundingClientRect().width+1/64,
      permissionLabelVisible:getComputedStyle(permission.querySelector('span')).display!=='none'};
    },density);
    samples.push({...sample,density,theme,geometry});
    await page.screenshot({path:path.join(output,`web-composer-layout-${sample.id}-${theme}-dpr${density}.png`)});
   }
   if(errors.length)throw new Error(errors.join('\n'));
  } finally {await context.close();}
 }
 await fs.writeFile(path.join(output,'web-composer-layout-dpr2.625.json'),JSON.stringify(samples,null,2)+'\n');
 console.log('Recorded 420dpi composed composer layouts, real textarea growth and live phone/keyboard/narrow/wide transitions in both themes and all three UI locales. Not a device or full-raster pass.');
}
