// Local fixture used by Go's protocol integration test. No project files or models.
import http from 'node:http';
import { defineSite } from '../src/server.mjs';
let executions=0;
const site=defineSite({name:'Independent fixture shop',tools:[{name:'opening_hours',description:'Read opening hours',parameters:{type:'object',properties:{day:{type:'string'}},required:['day'],additionalProperties:false},readOnly:true,execute:({day})=>{executions++;return{day,opens:'09:00'};}}]});
const server=http.createServer(async(req,res)=>{
  if(req.url==='/fixture-state'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({executions}));return;}
  const controller=new AbortController();res.on('close',()=>{if(!res.writableEnded)controller.abort();});
  try{
    const parts=[];let size=0;for await(const chunk of req){size+=chunk.length;if(size>1_100_000)throw new Error('body too large');parts.push(chunk);}
    const body=Buffer.concat(parts),request=new Request(`http://127.0.0.1:${server.address().port}${req.url}`,{method:req.method,headers:req.headers,...(body.length?{body}:{}),signal:controller.signal});
    const response=await site.handle(request);res.writeHead(response.status,Object.fromEntries(response.headers));res.end(Buffer.from(await response.arrayBuffer()));
  }catch{if(!res.destroyed){res.statusCode=500;res.end('fixture failed');}}
});
server.listen(0,'127.0.0.1',()=>console.log(`http://127.0.0.1:${server.address().port}`));
