// Read actual Pierre row focus, including the external search input's visual focus.
export async function fileFocusReference(page, name) {
 const records=[];
 const capture=async id=>{
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  records.push({id,...await page.locator('file-tree-container').evaluate(host=>{
   const root=host.shadowRoot,rows=[...root.querySelectorAll('[data-type="item"]')];
   const focused=rows.filter(row=>row.getAttribute('data-item-focused')==='true');
   if(focused.length>1)throw new Error('Unexpected multiple file-row focus rings');
   const row=focused[0],style=row&&getComputedStyle(row,'::before');
   return {paths:rows.map(row=>row.getAttribute('data-item-path')),focus:row?.getAttribute('data-item-path')||'',
    logicalFocus:rows.find(row=>row.tabIndex===0)?.getAttribute('data-item-path')||'',
    search:!!document.querySelector('.file-sidebar-search input'),searchFocused:document.activeElement===document.querySelector('.file-sidebar-search input'),
    ring:style?{width:parseFloat(style.outlineWidth),offset:parseFloat(style.outlineOffset),color:style.outlineColor,radius:parseFloat(style.borderRadius)}:null};
  })});
 };
 const toggle=page.locator('.file-sidebar-actions button').first();
 await capture('closed');
 await toggle.click();await capture('open-empty');
 const input=page.locator('.file-sidebar-search input');
 await input.fill('__spool_font_probe_no_match__');await capture('unmatched');
 await input.fill(name);await capture('match-cjk');
 await input.fill('plain');await capture('match-latin');
 await input.fill('');await capture('clear');
 await input.press('Escape');await capture('escape');
 const first=page.locator('file-tree-container [data-type="item"]').first();
 await first.focus();await first.press('ArrowDown');await capture('tree-keyboard');
 await toggle.focus();await capture('tree-blur');
 await toggle.click();await capture('reopen');
 await page.locator('.file-sidebar-search input').fill(name);await capture('rematch');
 await page.locator('.file-sidebar-actions button').nth(1).focus();await capture('search-blur');
 await toggle.click();await capture('close');
 return records;
}
