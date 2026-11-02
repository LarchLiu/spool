#!/usr/bin/env node
// Execute the installed Web controller; no native focus/order rules serve as the oracle.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath,pathToFileURL} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const {FileTree}=await import(pathToFileURL(path.join(root,'web/node_modules/@pierre/trees/dist/index.js')).href);
const {compareSegmentValues}=await import(pathToFileURL(path.join(root,'web/node_modules/@pierre/trees/dist/path-store/src/sort.js')).href);
const entries=(...names)=>names.map(name=>({name:name.replace(/\/$/,''),path:name.replace(/\/$/,''),kind:name.endsWith('/')?'directory':'text'}));
const operations=[
 {type:'load',path:'.',entries:entries('src/','docs/','empty/','apple.txt','readme.txt','z-last.txt')},
 {type:'focus',path:'z-last.txt'},
 {type:'open'},
 {type:'query',value:'src'},
 {type:'load',path:'src',entries:entries('src/alpha.txt','src/zeta.md','src/nested/').map(entry=>({...entry,name:entry.name.split('/').at(-1)}))},
 {type:'focus',path:'src/zeta.md'},
 {type:'query',value:' SRC '},
 {type:'query',value:'src\\nested'},
 {type:'load',path:'src/nested',entries:[{name:'child.txt',path:'src/nested/child.txt',kind:'text'}]},
 {type:'query',value:'alpha'},
 {type:'query',value:'__missing__'},
 {type:'close'},
 {type:'expand',path:'src'},
 {type:'expand',path:'src/nested'},
 {type:'focus',path:'src/nested/child.txt'},
 {type:'collapse',path:'src/nested'},
 {type:'collapse',path:'src'},
 {type:'open'},
 {type:'query',value:'empty'},
 {type:'load',path:'empty',entries:[{name:'later.txt',path:'empty/later.txt',kind:'text'}]},
 {type:'query',value:''},
 {type:'close'},
];
const model=new FileTree({paths:[],initialExpansion:'closed',flattenEmptyDirectories:false,search:false});
const cases=[];
for(const operation of operations) {
 switch(operation.type) {
  case 'load':model.batch(operation.entries.map(entry=>({type:'add',path:entry.path+(entry.kind==='directory'?'/':'')})));break;
  case 'focus':model.focusPath(operation.path);break;
  case 'open':model.openSearch();break;
  case 'close':model.closeSearch();break;
  case 'query':model.setSearch(operation.value);break;
  case 'expand':model.getItem(operation.path+'/').expand();break;
  case 'collapse':model.getItem(operation.path+'/').collapse();break;
  default:throw new Error('Unknown focus operation');
 }
 cases.push({operation,focus:model.getFocusedPath()?.replace(/\/$/,'')||'',rows:model.getVisibleRows(0,model.getVisibleCount()).map(row=>({path:row.path.replace(/\/$/,''),expanded:row.isExpanded}))});
}
const contents=JSON.stringify({purpose:'Installed actual Web FileTree controller across lazy listings, normalized queries and hidden descendants; not UI raster evidence',cases},null,2)+'\n';
const file=path.join(root,'android/app/src/test/resources/web-file-tree-focus.json');
if(process.argv.includes('--check')) {
 if(fs.readFileSync(file,'utf8')!==contents)throw new Error('Web file-tree focus vectors are stale');
 console.log(`Verified ${cases.length} actual Web lazy-tree focus vectors.`);
} else {
 fs.writeFileSync(file,contents);
 console.log(`Recorded ${cases.length} actual Web lazy-tree focus vectors.`);
}
const names=['a.js','a  b','a','A','a2','A2','a02','a002','a10','a1b10','a1b2','!first','.gitignore','_last','Z','z',
 '0','00','01','1','2','10','1.2','1.10','-1','+2','1e2','1e10','1e02',
 '9007199254740991','9007199254740992','9007199254740993','100000000000000000000','100000000000000000001',
 '9'.repeat(310),'1'+'0'.repeat(310),'infinity','h','j','i','İ','i\u0307','ß','ẞ','σ','Σ','ς','ΟΣ','οσ','ος',
 'e\u0301','é','共享2','共享10','共有2','共有10','😀2','😀10','２','１０'];
const comparisons=names.map(left=>names.map(right=>Math.sign(compareSegmentValues(left,right))));
const orderCases=[];
for(const reverse of [false,true]) {
 const paths=[...names,...['dir2/','dir10/','dir02/','Dir2/','!dir/','.dir/'],
  ...['dir2/a10.txt','dir2/a2.txt','dir10/z.txt','dir02/共享10','dir02/共享2']];
 if(reverse)paths.reverse();
 const tree=new FileTree({paths,initialExpansion:'open',flattenEmptyDirectories:false,search:false});
 const sample={paths,rows:tree.getVisibleRows(0,tree.getVisibleCount()).map(row=>row.path),searches:[]};
 tree.openSearch();
 for(const query of ['a','dir','2','__missing__','']) {
  tree.setSearch(query);
  sample.searches.push({query,focus:tree.getFocusedPath()?.replace(/\/$/,'')||'',rows:tree.getVisibleRows(0,tree.getVisibleCount()).map(row=>row.path)});
 }
 orderCases.push(sample);
}
const orderFile=path.join(root,'android/app/src/test/resources/web-file-order.json');
const orderContents=JSON.stringify({purpose:'Actual installed Pierre comparator and complete FileTree order/search, independent of native rules',names,comparisons,cases:orderCases},null,2)+'\n';
if(process.argv.includes('--check')) {
 if(fs.readFileSync(orderFile,'utf8')!==orderContents)throw new Error('Web file order vectors are stale');
 console.log(`Verified ${names.length**2} actual Web pair comparisons and ${orderCases.length} full trees/search sequences.`);
} else {
 fs.writeFileSync(orderFile,orderContents);
 console.log(`Recorded ${names.length**2} actual Web pair comparisons and ${orderCases.length} full trees/search sequences.`);
}
