import fs from 'node:fs/promises';
import path from 'node:path';

export async function renderSendButtonReferences({browser,url,output}) {
 for(const width of [320,360,412])for(const theme of ['light','dark'])for(const locale of ['en','zh-CN','ja']) {
  const context=await browser.newContext({viewport:{width,height:328},deviceScaleFactor:1});
  try {
   const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)));
   await page.goto(`${url}?screen=send-button&locale=${locale}`);await page.waitForSelector('.send-button-scene .send');
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   const samples=[];
   for(const time of [0,200,400,600]) {
    await page.evaluate(time=>{for(const animation of document.getAnimations()){animation.pause();animation.currentTime=time;}},time);
    const states=await page.evaluate(()=>{
     const box=e=>{const r=e.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};
     return [...document.querySelectorAll('[data-case]')].map(row=>{
      const button=row.querySelector('button'),glyph=button.querySelector('svg'),style=getComputedStyle(button);
      const rgb=style.backgroundColor.match(/[\d.]+/g).map(Number),matrix=new DOMMatrix(getComputedStyle(glyph).transform);
      const angle=(Math.atan2(matrix.b,matrix.a)*180/Math.PI+360)%360;
      return {id:row.dataset.case,button:box(button),glyph:box(glyph),label:button.getAttribute('aria-label'),enabled:!button.disabled,
       background:(0xff000000|rgb[0]<<16|rgb[1]<<8|rgb[2])|0,angle};
     });
    });
    samples.push({time,states});
    await page.screenshot({path:path.join(output,`web-send-button-${width}-${theme}-${locale}-${time}.png`)});
   }
   if(errors.length)throw new Error(errors.join('\n'));
   await fs.writeFile(path.join(output,`web-send-button-${width}-${theme}-${locale}.json`),JSON.stringify(samples,null,2)+'\n');
  } finally {await context.close();}
 }
 console.log('Rendered the production ComposerSendButton, including actual 800ms CSS spin samples.');
}
