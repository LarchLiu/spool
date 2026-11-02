// Read actual collapsed-table text paint, including fractional row origins and ancestor moves.
export const tableOriginFixtures=[
 {locale:'en',headings:['Name','Value'],labels:['First','Second','Third','Fourth','Fifth','Sixth']},
 {locale:'zh-CN',headings:['名称','数值'],labels:['第一行','第二行','第三行','第四行','第五行','第六行']},
 {locale:'ja',headings:['名前','値'],labels:['一行目','二行目','三行目','四行目','五行目','六行目']},
].map(({locale,headings,labels})=>({id:locale,locale,source:`| ${headings[0]} | ${headings[1]} |\n| --- | ---: |\n`+
 labels.map((label,index)=>`| [${label}](https://example.com/${index}) | ${index+1} |`).join('\n')}));
tableOriginFixtures.push({id:'wrapped',locale:'en',source:'| Name | Value |\n| --- | ---: |\n'+[
 'A long linked label that wraps across several native text lines while keeping its original selection and link behavior.',
 'Short',
 'Another long linked label with enough words to exercise centered cells next to a wrapped paragraph.',
 'Small',
 'The last longer label checks that fractional row heights keep accumulating correctly instead of drifting down the table.',
 'End',
].map((label,index)=>`| [${label}](https://example.com/${index}) | ${index+1} |`).join('\n')});

export async function measureTableOrigins(browser,{css,fontCSS,renderMarkdown,output}) {
 const cases=[];
 for(const density of [1,2.625,3]) {
  const context=await browser.newContext({viewport:{width:360,height:600},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());
   const page=await context.newPage(),client=await context.newCDPSession(page);let layers=[];
   client.on('LayerTree.layerTreeDidChange',event=>{if(event.layers)layers=event.layers;});
   await client.send('LayerTree.enable');
   for(const fixture of tableOriginFixtures) {
    await page.setContent(`<!doctype html><html data-theme="light" lang="${fixture.locale}"><style>${css}</style><style>${fontCSS}
     #table-shell{position:relative}#table-flow{width:360px;padding:25px 18px 0}
     </style><div id="table-shell"><div id="table-flow"><div class="assistant-text">${renderMarkdown(fixture.source)}</div></div></div></html>`);
    await page.evaluate(()=>document.fonts.ready);
    for(const padding of [25,25.484375,25.5])for(const offsetPhysical of [0,1,3]) {
     await page.evaluate(({padding,offsetPhysical,density})=>{
      document.querySelector('#table-flow').style.paddingTop=padding+'px';
      document.querySelector('#table-shell').style.top=offsetPhysical/density+'px';
     },{padding,offsetPhysical,density});
     const geometry=await page.evaluate(()=>{
      const bounds=node=>{const r=node.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height};};
      return {table:bounds(document.querySelector('table')),cells:[...document.querySelectorAll('th,td')].map(cell=>{
       const lines=[],walker=document.createTreeWalker(cell,NodeFilter.SHOW_TEXT);
       for(let node=walker.nextNode();node;node=walker.nextNode())for(let i=0;i<node.length;i++) {
        const range=document.createRange();range.setStart(node,i);range.setEnd(node,i+1);
        const rect=range.getBoundingClientRect();
        if(!rect.width)continue;
        let line=lines.find(line=>Math.abs(line.top-rect.top)<.01);
        if(!line){line={top:rect.top,text:''};lines.push(line);}
        line.text+=node.textContent[i];
       }
       return {text:cell.innerText,...bounds(cell),font:getComputedStyle(cell).font,lines:lines.map(line=>line.text.trim())};
      })};
     });
     await page.screenshot(padding===25&&offsetPhysical===0?{path:`${output}/web-table-origin-${fixture.id}-dpr${density}.png`}:{});
     const text=[];
     for(const layer of layers.filter(layer=>layer.drawsContent&&layer.width>0&&layer.height>0)) {
      const {snapshotId}=await client.send('LayerTree.makeSnapshot',{layerId:layer.layerId});
      try {
       const {commandLog}=await client.send('LayerTree.snapshotCommandLog',{snapshotId});
       text.push(...commandLog.filter(command=>command.method==='drawTextBlob').map(command=>command.params));
      } finally {await client.send('LayerTree.releaseSnapshot',{snapshotId});}
     }
     for(const cell of geometry.cells) {
      cell.baselines=[...new Set(text.filter(paint=>paint.x>=cell.x&&paint.x<cell.x+cell.width&&paint.y>=cell.y&&paint.y<cell.y+cell.height).map(paint=>paint.y))];
      cell.baselines.sort((a,b)=>a-b);
      if(!cell.baselines.length)throw new Error(`Missing actual table text paint: ${fixture.id}/${density}/${cell.text}`);
     }
     cases.push({id:fixture.id,locale:fixture.locale,density,padding,offsetPhysical,...geometry});
    }
   }
  } finally {await context.close();}
 }
 return {fixtures:tableOriginFixtures,cases};
}
