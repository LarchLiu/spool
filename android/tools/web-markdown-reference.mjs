// Compile the actual Web renderer once for native reference generators, not a parallel renderer.
import path from 'node:path';
import vm from 'node:vm';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(root,'web/package.json'));
const {buildSync}=require('esbuild'),React=require('react'),{renderToStaticMarkup}=require('react-dom/server');
export const {parseFragment}=await import(require.resolve('parse5'));
const module={exports:{}};
const built=buildSync({stdin:{contents:`export {MarkdownMessage} from ${JSON.stringify(path.join(root,'web/src/markdown-message.tsx'))}; export {LocalFileContext} from ${JSON.stringify(path.join(root,'web/src/local-file-context.tsx'))};`,resolveDir:root,loader:'tsx'},bundle:true,write:false,format:'cjs',platform:'node',external:['react','react/*'],logLevel:'error'});
vm.runInNewContext(built.outputFiles[0].text,{module,exports:module.exports,require,console,URL},{filename:'markdown-message.tsx'});
export function renderMarkdown(source,{localFiles=false}={}) {
 let node=React.createElement(module.exports.MarkdownMessage,null,source);
 if(localFiles)node=React.createElement(module.exports.LocalFileContext.Provider,{value:{openFile(){}}},node);
 return renderToStaticMarkup(node);
}
