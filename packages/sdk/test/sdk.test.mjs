import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, symlinkSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { spawnSync, spawn } from 'node:child_process';
import { z } from 'zod';
import { discoverTools } from '../src/discovery.mjs';
import { defineSite, defineTool, adaptTool, TUNNEL_PROTOCOL } from '../src/server.mjs';

const origin='https://service.example.com', prefix='/api/jase-tunnel/v1/';
const model={key:'ollama:fixture',name:'Fixture LLM',engine:'ollama',tools:true,contextTokens:8192};
const req=(action,data,headers={})=>new Request(origin+prefix+action,{method:data===undefined?'GET':'POST',headers:{host:'service.example.com',...(data!==undefined?{'content-type':'application/json'}:{}),...headers},...(data!==undefined?{body:JSON.stringify(data)}:{})});
async function pair(site) {
 const response=await site.handle(req('tickets',{protocol:TUNNEL_PROTOCOL},{origin}));assert.equal(response.status,200);
 const cookie=response.headers.get('set-cookie').split(';')[0],ticket=(await response.json()).ticket;
 const paired=await site.handle(req('pair',{protocol:TUNNEL_PROTOCOL,ticket,label:'Fixture computer',models:[model]}));assert.equal(paired.status,200);
 const device=await paired.json();
 const session=await (await site.handle(req('session',undefined,{cookie}))).json();
 return {...device,cookie,model:session.models[0].id};
}
const browser=(p)=>({cookie:p.cookie,origin});
const desktop=(p)=>({authorization:'Bearer '+p.token});
async function next(site,p) { const r=await site.handle(req('poll',{},desktop(p)));assert.equal(r.status,200);const task=(await r.json()).task;assert.ok(task);return task; }
async function reply(site,p,task,result) {const r=await site.handle(req('result',{id:task.id,result},desktop(p)));assert.equal(r.status,200);}
function fixture(t) {const root=mkdtempSync(join(tmpdir(),'jase-sdk-cli-'));t.after(()=>rmSync(root,{recursive:true,force:true}));return root;}

test('CLI discovers declared tools without importing source, following symlinks or reading private data',t=>{
 const root=fixture(t);mkdirSync(join(root,'src'));mkdirSync(join(root,'data'));mkdirSync(join(root,'node_modules'));
 writeFileSync(join(root,'src','tools.ts'),`import { tool as aiTool } from 'ai';\nthrow new Error('MUST NOT EXECUTE');\nexport const searchProducts = aiTool({ description: 'Search', inputSchema: z.object({query:z.string()}), execute: async () => [] });\nconst hidden=aiTool({description:'Hidden',inputSchema:{},execute(){}});\nexport const aiTools={ weather: {description:'Weather',parameters:{type:'object'},execute(){}} };`);
 writeFileSync(join(root,'data','secret.ts'),`export const secret={description:'secret',parameters:{type:'object'},execute(){}}`);
 writeFileSync(join(root,'node_modules','bad.ts'),'throw new Error("never");');
 try{symlinkSync(join(root,'data'),join(root,'linked'));}catch(error){if(error.code!=='EPERM')throw error;}
 const scan=discoverTools(root);assert.deepEqual(scan.tools.map(t=>t.name).sort(),['search_products','weather']);assert.equal(scan.files,1);
});

test('CLI init configures Next.js, is repeatable, adds and removes tools and preserves existing routes',t=>{
 const root=fixture(t);mkdirSync(join(root,'src','app'),{recursive:true});mkdirSync(join(root,'src','tools'));
 writeFileSync(join(root,'package.json'),JSON.stringify({name:'fixture',dependencies:{next:'15.5.27'}}));writeFileSync(join(root,'tsconfig.json'),'{}');
 writeFileSync(join(root,'src','app','layout.tsx'),`import React from 'react';\nexport default function Layout({children}) {return <html><body>{children}</body></html>}`);
 writeFileSync(join(root,'src','tools','catalog.ts'),`export const catalog={name:'catalog',description:'Catalog',parameters:{type:'object'},execute(){return []}};`);
 const cli=resolve('bin/cli.mjs'),run=(...args)=>spawnSync(process.execPath,[cli,...args,'--cwd',root],{encoding:'utf8'});
 assert.equal(run('init','--skip-install','--dry-run').status,0);assert.equal(existsSync(join(root,'jase-tunnel.config.ts')),false);
 assert.equal(run('init','--skip-install').status,0);assert.ok(existsSync(join(root,'src/app/.well-known/jase-tunnel.json/route.ts')));
 const layout=readFileSync(join(root,'src/app/layout.tsx'),'utf8');assert.equal((layout.match(/<JaseTunnel/g)??[]).length,1);
 assert.equal(run('init','--skip-install').status,0);assert.equal(readFileSync(join(root,'src/app/layout.tsx'),'utf8'),layout);
 assert.equal(run('tools','remove','catalog').status,0);assert.equal(run('tools','scan').status,0);assert.equal(JSON.parse(readFileSync(join(root,'jase-tunnel.tools.json'))).tools[0].enabled,false);
 assert.equal(run('tools','add','./src/tools/catalog.ts#catalog','--name','manual_catalog').status,0);
 const route=join(root,'src/app/api/jase-tunnel/v1/[...path]/route.ts');writeFileSync(route,'OWNER ROUTE');
 const failed=run('init','--skip-install');assert.equal(failed.status,1);assert.match(failed.stderr,/esiste già/);assert.equal(readFileSync(route,'utf8'),'OWNER ROUTE');
});

test('schema and authorization checks run before an executor',async()=>{
 let calls=0;const tool=adaptTool({name:'lookup',description:'Read a value',inputSchema:z.object({id:z.number().int()}),authorize:ctx=>ctx.request.headers.get('x-user')==='owner',execute:()=>++calls});
 const context={request:req('chat'),signal:new AbortController().signal,callId:'fixture'};
 await assert.rejects(tool.execute({id:'1'},context));await assert.rejects(tool.execute({id:1},context));assert.equal(calls,0);
 assert.equal(await tool.execute({id:1},{...context,request:req('chat',undefined,{'x-user':'owner'})}),1);
});

test('two independent services keep sessions isolated and complete real tool round trips',async()=>{
 let reads=0;const tools=[defineTool({name:'inventory',description:'Read inventory',parameters:{type:'object',properties:{sku:{type:'string'}},required:['sku'],additionalProperties:false},readOnly:true,execute:({sku},ctx)=>{assert.equal(ctx.request.headers.get('cookie').startsWith('jase_tunnel='),true);reads++;return{sku,count:7};}})];
 const a=defineSite({name:'Shop A',origin,tools}),b=defineSite({name:'Shop B',origin,tools});
 const alice=await pair(a),bob=await pair(a),other=await pair(b);
 assert.equal((await b.handle(req('active',undefined,desktop(alice)))).status,401);
 assert.equal((await a.handle(req('chat',{model:alice.model,message:'read'},browser(bob)))).status,403);
 assert.equal((await a.handle(req('chat',{model:alice.model,message:'read'},{cookie:alice.cookie,origin:'https://evil.example.com'}))).status,403);
 const answer=a.handle(req('chat',{model:alice.model,message:'stock?'},browser(alice)));
 const first=await next(a,alice);assert.equal(first.tools[0].name,'inventory');
 const firstResult={content:'',toolCalls:[{name:'inventory',arguments:{sku:'ABC'}}]};
 await reply(a,alice,first,firstResult);
 assert.equal((await a.handle(req('result',{id:first.id,result:firstResult},desktop(alice)))).status,200);
 assert.equal((await a.handle(req('result',{id:first.id,result:{content:'different',toolCalls:[]}},desktop(alice)))).status,409);
 const second=await next(a,alice);assert.match(second.messages.at(-1).content,/"count":7/);
 await reply(a,alice,second,{content:'Seven available.',toolCalls:[]});
 assert.equal((await (await answer).json()).content,'Seven available.');assert.equal(reads,1);
 assert.equal((await a.handle(req('poll',{}, {origin,...desktop(alice)}))).status,403);
 await a.handle(req('disconnect',{},browser(alice)));assert.equal((await a.handle(req('active',undefined,desktop(alice)))).status,401);
 assert.notEqual(other.token,bob.token);
});

test('tools with unknown effects require a one-use confirmation and are not run on discovery',async()=>{
 let writes=0;const site=defineSite({origin,tools:[{name:'update_order',description:'Update order',parameters:{type:'object'},execute:()=>++writes}]});
 assert.equal((await site.manifest().json()).protocols[0],TUNNEL_PROTOCOL);assert.equal(writes,0);
 const p=await pair(site),question=site.handle(req('chat',{model:p.model,message:'Update'},browser(p)));
 const task=await next(site,p);await reply(site,p,task,{content:'',toolCalls:[{name:'update_order',arguments:{}}]});
 const pending=await (await question).json();assert.ok(pending.confirmation);assert.equal(writes,0);
 const wrong=await pair(site);assert.equal((await site.handle(req('confirm',{id:pending.confirmation.id,allow:true},browser(wrong)))).status,410);
 const approved=site.handle(req('confirm',{id:pending.confirmation.id,allow:true},browser(p)));
 const nextTask=await next(site,p);assert.equal(writes,1);await reply(site,p,nextTask,{content:'Done',toolCalls:[]});assert.equal((await approved).status,200);
 assert.equal((await site.handle(req('confirm',{id:pending.confirmation.id,allow:true},browser(p)))).status,410);assert.equal(writes,1);
});

test('CLI init wires an Express server and its pages once, before other middleware',t=>{
 const root=fixture(t);mkdirSync(join(root,'public'));
 writeFileSync(join(root,'package.json'),JSON.stringify({name:'express-fixture',dependencies:{express:'5.1.0'}}));
 writeFileSync(join(root,'server.js'),`'use strict';\nconst express = require('express');\nconst app = express();\napp.use(express.json());\napp.use(express.static('public'));\napp.listen(3000);\n`);
 writeFileSync(join(root,'public','index.html'),'<!doctype html><html><body><h1>Shop</h1></body></html>');
 const cli=resolve('bin/cli.mjs'),run=(...args)=>spawnSync(process.execPath,[cli,...args,'--cwd',root],{encoding:'utf8'});
 const result=run('init','--skip-install');assert.equal(result.status,0,result.stderr);
 const server=readFileSync(join(root,'server.js'),'utf8');
 assert.match(server,/import\("\.\/jase-tunnel\.config\.mjs"\)/);
 assert.ok(server.indexOf('app.use((req, res, next) => jaseTunnel')>server.indexOf('const app = express();'));
 assert.ok(server.indexOf('app.use((req, res, next) => jaseTunnel')<server.indexOf('app.use(express.json())'));
 const page=readFileSync(join(root,'public','index.html'),'utf8');assert.equal((page.match(/client\/widget\.mjs/g)??[]).length,1);
 assert.equal(run('init','--skip-install').status,0);
 assert.equal(readFileSync(join(root,'server.js'),'utf8'),server);assert.equal(readFileSync(join(root,'public','index.html'),'utf8'),page);
 assert.ok(existsSync(join(root,'.jase-tunnel','backups')));
});

test('CLI init wraps a node:http handler: manifest and panel are served, other requests reach the app',async t=>{
 const root=fixture(t);
 writeFileSync(join(root,'package.json'),JSON.stringify({name:'http-fixture',type:'module'}));
 writeFileSync(join(root,'server.mjs'),`import http from 'node:http';\nconst server = http.createServer((req, res) => { res.end('app ' + req.url); });\nserver.listen(0, '127.0.0.1', () => console.log(server.address().port));\n`);
 const cli=resolve('bin/cli.mjs');
 const result=spawnSync(process.execPath,[cli,'init','--skip-install','--cwd',root],{encoding:'utf8'});assert.equal(result.status,0,result.stderr);
 assert.match(result.stdout,/Aggiungi il pannello alle tue pagine/);
 assert.match(readFileSync(join(root,'server.mjs'),'utf8'),/http\.createServer\(withJaseTunnel\(\(req, res\) => \{ res\.end/);
 mkdirSync(join(root,'node_modules'));symlinkSync(resolve('.'),join(root,'node_modules','jase-tunnel'),'dir');
 const child=spawn(process.execPath,[join(root,'server.mjs')],{cwd:root});
 const closed=new Promise(done=>child.once('close',done));
 // Windows cannot remove a running process's working directory. Stop the server
 // before fixture(t)'s cleanup runs, including when startup or an assertion fails.
 try {
 const port=await new Promise((done,fail)=>{child.stdout.once('data',d=>done(String(d).trim()));child.once('exit',code=>fail(new Error('server exited '+code)));});
 const base=`http://127.0.0.1:${port}`;
 const manifest=await (await fetch(base+'/.well-known/jase-tunnel.json')).json();
 assert.equal(manifest.protocols[0],TUNNEL_PROTOCOL);assert.equal(manifest.name,'http-fixture');
 const widget=await fetch(base+'/api/jase-tunnel/v1/client/widget.mjs');
 assert.equal(widget.status,200);assert.match(widget.headers.get('content-type'),/javascript/);assert.match(await widget.text(),/mountJaseTunnel/);
 assert.equal(await (await fetch(base+'/shop?x=1')).text(),'app /shop?x=1');
 } finally {
  child.kill();
  await closed;
 }
});

test('a grant revoked in the desktop app disappears from the browser session at once',async()=>{
 const site=defineSite({origin,tools:[]}),p=await pair(site);
 assert.equal((await (await site.handle(req('session',undefined,{cookie:p.cookie}))).json()).paired,true);
 assert.equal((await site.handle(req('revoke',{},desktop(p)))).status,200);
 assert.equal((await (await site.handle(req('session',undefined,{cookie:p.cookie}))).json()).paired,false);
 assert.equal((await site.handle(req('poll',{},desktop(p)))).status,401);
});
