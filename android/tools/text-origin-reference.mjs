// Actual MarkdownMessage paint baselines as ancestor positions and fractional flow insets change.
export async function measureTextOrigins(browser,{css,fontCSS,markup}) {
 const cases=[];
 for(const density of [1,2.625,3]) {
  const context=await browser.newContext({viewport:{width:360,height:240},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());
   const page=await context.newPage(),client=await context.newCDPSession(page);let layers=[];
   client.on('LayerTree.layerTreeDidChange',event=>{if(event.layers)layers=event.layers;});await client.send('LayerTree.enable');
   for(const padding of [25,25.484375,25.5])for(const offsetPhysical of [0,1,2,3,7]) {
    layers=[];
    await page.setContent(`<!doctype html><html data-theme="light"><style>${css}</style><style>${fontCSS}
     #origin-shell{position:relative;top:${offsetPhysical/density}px}
     #origin-flow{padding:${padding}px 18px 0;width:360px;height:240px}
     </style><div id="origin-shell"><div id="origin-flow"><div class="assistant-text" id="origin-text">${markup}</div></div></div></html>`);
    await page.evaluate(()=>document.fonts.ready);
    const geometry=await page.locator('#origin-text').evaluate(row=>{
     const bounds=row.getBoundingClientRect();return {y:bounds.y,height:bounds.height,text:row.innerText};
    });
    await page.screenshot();
    const baselines=[];
    for(const layer of layers.filter(layer=>layer.drawsContent&&layer.width>0&&layer.height>0)) {
     const {snapshotId}=await client.send('LayerTree.makeSnapshot',{layerId:layer.layerId});
     try {
      const {commandLog}=await client.send('LayerTree.snapshotCommandLog',{snapshotId});
      for(const command of commandLog)if(command.method==='drawTextBlob'&&command.params.y>=geometry.y&&command.params.y<geometry.y+geometry.height)baselines.push(command.params.y);
     } finally {await client.send('LayerTree.releaseSnapshot',{snapshotId});}
    }
    const painted=[...new Set(baselines)].sort((a,b)=>a-b);
    if(painted.length!==2)throw new Error('Expected two actually painted Web text baselines');
    cases.push({density,padding,offsetPhysical,...geometry,baselines:painted});
   }
  } finally {await context.close();}
 }
 return cases;
}
