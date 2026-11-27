// Run the current Linux capture package on an owned virtual display in box.
// Invoke through tools/run.sh; requires an already authenticated box CLI.
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const box = process.argv[2] || 'main';
const run = process.env.SPOOL_RUN_ROOT;
if (!run) throw new Error('Run through sh tools/run.sh screenshot-linux-box bun tools/screenshot-linux-box.mjs [box]');

function command(executable, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { ...options, stdio: ['pipe', 'pipe', 'pipe'] });
    const stdout = [], stderr = [];
    child.stdout.on('data', chunk => stdout.push(chunk));
    child.stderr.on('data', chunk => stderr.push(chunk));
    child.on('error', reject);
    child.stdin.on('error', error => { if (error.code !== 'EPIPE') reject(error); });
    child.stdin.end(options.input);
    child.on('close', code => resolve({ code, stdout: Buffer.concat(stdout).toString(), stderr: Buffer.concat(stderr).toString() }));
  });
}

const binary = path.join(run, 'screencapture.test');
const build = await command('go', ['test', '-c', '-ldflags=-s -w', '-o', binary, './internal/screencapture'], {
  env: { ...process.env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' },
});
if (build.code !== 0) throw new Error(build.stderr || build.stdout);
const data = await readFile(binary);
const hash = createHash('sha256').update(data).digest('hex');

// Only this program and the compiled capture tests cross the box connection.
// No checkout, application data, or credentials are transferred.
const remote = String.raw`
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
let run, server, serverClosed, interrupted;
const children=new Set();
const report = {platform:process.platform, arch:process.arch};
for(const signal of ['SIGINT','SIGTERM']) process.once(signal,()=>{
  interrupted=Error('Interrupted by '+signal);
  for(const child of children) try{process.kill(-child.pid,'SIGTERM');}catch{}
  if(server) server.kill('SIGTERM');
  process.stdin.destroy(interrupted);
});
process.stdin.on('error',()=>{});
async function command(cmd,args,env) {
  if(interrupted) throw interrupted;
  return new Promise((resolve,reject)=>{
    // A supervised process group keeps the test and its D-Bus daemon together
    // when the owning runner is interrupted. The runner always awaits it.
    const child=spawn(cmd,args,{env,detached:true,stdio:['ignore','pipe','pipe']});
    children.add(child);
    const out=[],err=[];
    child.stdout.on('data',b=>out.push(b)); child.stderr.on('data',b=>err.push(b));
    child.on('error',error=>{children.delete(child);reject(error);});
    child.on('close',code=>{children.delete(child);resolve({code,stdout:Buffer.concat(out).toString(),stderr:Buffer.concat(err).toString()});});
  });
}
(async()=>{
  if(process.platform!=='linux'||process.arch!=='x64') throw Error('Expected Linux x64 box');
  const user=os.userInfo();
  const parent=path.join(os.tmpdir(),'spool-'+user.username.replace(/[^a-zA-Z0-9_-]/g,'_'),'spool');
  await fs.mkdir(parent,{recursive:true,mode:0o700});
  for(const dir of [path.dirname(parent),parent]) {
    const stat=await fs.lstat(dir);
    if(!stat.isDirectory()||stat.uid!==user.uid||(stat.mode&0o077)!==0) throw Error('Unsafe scratch parent: '+dir);
  }
  run=await fs.mkdtemp(path.join(parent,'screenshot-linux-box.'));
  const tmp=path.join(run,'tmp'); await fs.mkdir(tmp,{mode:0o700});
  const chunks=[]; for await(const chunk of process.stdin) chunks.push(chunk);
  if(interrupted) throw interrupted;
  const data=Buffer.concat(chunks);
  const sha256=crypto.createHash('sha256').update(data).digest('hex');
  if(sha256!==process.argv[1]) throw Error('Transferred binary checksum mismatch');
  report.sha256=sha256;
  const binary=path.join(run,'screencapture.test');
  await fs.writeFile(binary,data,{mode:0o700});
  const authority=path.join(run,'Xauthority');
  const cookie=crypto.randomBytes(16).toString('hex');
  await fs.writeFile(authority,'',{mode:0o600});
  const env={...process.env,TMPDIR:tmp,TMP:tmp,TEMP:tmp,SPOOL_RUN_ROOT:run,XAUTHORITY:authority,XDG_SESSION_TYPE:'x11',WAYLAND_DISPLAY:''};
  let auth=await command('xauth',['-f',authority,'add',':0','.',cookie],env);
  if(auth.code!==0) throw Error(auth.stderr);
  const config=path.join(run,'xorg.conf');
  await fs.writeFile(config,
    'Section "ServerFlags"\n Option "AutoAddDevices" "false"\n Option "DontVTSwitch" "true"\nEndSection\n'+
    'Section "Device"\n Identifier "dummy"\n Driver "dummy"\n VideoRam 16384\nEndSection\n'+
    'Section "Monitor"\n Identifier "monitor"\n HorizSync 5-100\n VertRefresh 5-200\n Modeline "800x600" 40 800 840 968 1056 600 601 605 628\nEndSection\n'+
    'Section "Screen"\n Identifier "screen"\n Device "dummy"\n Monitor "monitor"\n DefaultDepth 24\n SubSection "Display"\n Depth 24\n Virtual 800 600\n Modes "800x600"\n EndSubSection\nEndSection\n');
  const xorg='/usr/lib/xorg/Xorg';
  server=spawn(xorg,['-displayfd','3','-config',config,'-auth',authority,'-noreset','-nolisten','tcp','-logfile',path.join(run,'Xorg.log')],{env,stdio:['ignore','pipe','pipe','pipe']});
  const serverOutput=[];
  server.stdout.on('data',b=>serverOutput.push(b)); server.stderr.on('data',b=>serverOutput.push(b));
  serverClosed=once(server,'close');
  let timer;
  try {
    const display=await Promise.race([
      new Promise((resolve,reject)=>{
        let text=''; server.stdio[3].on('data',b=>{text+=b; if(text.includes('\n'))resolve(text.trim());});
        server.on('error',reject);
      }),
      serverClosed.then(()=>{throw Error('Xorg exited: '+Buffer.concat(serverOutput).toString());}),
      new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('Xorg readiness timeout')),10000);}),
    ]);
    if(!/^\d+$/.test(display)) throw Error('Invalid display number');
    report.display=':'+display;
    report.server='Xorg dummy (isolated, authenticated, TCP disabled)';
    auth=await command('xauth',['-f',authority,'add',report.display,'.',cookie],env);
    if(auth.code!==0) throw Error(auth.stderr);
  } finally {clearTimeout(timer);}
  const result=await command('dbus-run-session',['--',binary,'-test.v','-test.timeout=45s'],{
    ...env,DISPLAY:report.display,SPOOL_SCREENSHOT_XVFB:'1',SPOOL_SCREENSHOT_PORTAL_FIXTURE:'1',
  });
  report.exitCode=result.code; report.log=result.stdout+result.stderr;
  report.passed=(result.stdout.match(/^--- PASS:/gm)||[]).length;
  report.skipped=(result.stdout.match(/^--- SKIP:/gm)||[]).length;
  if(result.code!==0) process.exitCode=1;
})().catch(async error=>{
  report.error=error.stack;process.exitCode=1;
  if(run) try{report.xorgLog=await fs.readFile(path.join(run,'Xorg.log'),'utf8');}catch{}
}).finally(async()=>{
  if(server&&server.exitCode===null&&server.signalCode===null){
    server.kill('SIGTERM');
    const timer=setTimeout(()=>server.kill('SIGKILL'),3000);
    try{await serverClosed;}finally{clearTimeout(timer);}
  }
  if(run){await fs.rm(run,{recursive:true,force:true});report.cleaned=true;}
  console.log(JSON.stringify(report));
});
`;

const result = await command('bunx', ['--yes', '@simular-ai/box', 'exec', '--box', box, '--', 'node', '-e', remote, hash], { input: data });
const artifacts = path.resolve('build/artifacts/screenshot-linux-box');
await mkdir(artifacts, { recursive: true });
await writeFile(path.join(artifacts, 'native.log'), result.stdout + result.stderr);
let report;
try { report = JSON.parse(result.stdout.trim().split('\n').at(-1)); }
catch { throw new Error(result.stderr || result.stdout || `box exited ${result.code}`); }
await writeFile(path.join(artifacts, 'report.json'), JSON.stringify(report, null, 2) + '\n');
console.log(report.log || report.error || report);
if (report.xorgLog) console.log(report.xorgLog);
console.log(JSON.stringify({ exitCode: result.code, passed: report.passed, skipped: report.skipped, cleaned: report.cleaned, report: path.join(artifacts, 'report.json') }));
if (result.code !== 0 || report.exitCode !== 0) process.exitCode = 1;
