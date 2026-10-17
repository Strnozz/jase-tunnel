'use client';
import { createElement as h, useEffect, useRef, useState } from 'react';
import { TunnelClient } from './browser.mjs';

const button={border:'1px solid #ddd',borderRadius:10,padding:'9px 12px',background:'#fff',color:'#222',cursor:'pointer',font:'inherit'};
export function JaseTunnel({path='/api/jase-tunnel/v1/'}={}) {
  const [open,setOpen]=useState(false),[message,setMessage]=useState('Collega la tua LLM, senza registrazione.'),[session,setSession]=useState(null),[release,setRelease]=useState(null),[platform,setPlatform]=useState(null),[input,setInput]=useState(''),[history,setHistory]=useState([]),[pending,setPending]=useState(null),[busy,setBusy]=useState(false);
  const client=useRef();
  if(!client.current) client.current=new TunnelClient({path,onState:s=>{if(s.state==='awaiting_approval')setMessage('Conferma il dominio e scegli il modello nell’app Jase Tunnel.');}});
  useEffect(()=>{
    let active=true;
    client.current.platform().then(value=>{if(active)setPlatform(value);});
    client.current.releases().then(value=>{if(active)setRelease(value);}).catch(()=>{});
    client.current.session().then(value=>{if(active)setSession(value);}).catch(()=>{});
    // Previously opted-in visible pages can announce themselves. No inference is granted here.
    if(localStorage.getItem('jase-tunnel-enabled:'+location.origin)==='true' && document.visibilityState!=='hidden') client.current.discover().catch(()=>{});
    return()=>{active=false;};
  },[path]);
  const act=async fn=>{setBusy(true);try{await fn();}catch(error){setMessage(error.message);}finally{setBusy(false);}};
  const answer=result=>{if(result.confirmation){setPending(result.confirmation);setMessage('Lo strumento richiede la tua conferma.');}else{setPending(null);setHistory(old=>[...old,{role:'Jase Tunnel',text:result.content}]);setMessage('Risposta dal tuo modello locale.');}};
  const model=session?.models?.find(m=>m.available);
  const downloads=release?.downloads?.filter(d=>d.available)??[];
  const detected=downloads.find(d=>d.id===platform?.id);
  return h('aside',{'aria-label':'Jase Tunnel',style:{position:'fixed',right:20,bottom:20,zIndex:2147483000,fontFamily:'system-ui,sans-serif',fontSize:14,lineHeight:1.5,color:'#222'}},
    open&&h('section',{'aria-label':'Pannello Jase Tunnel',style:{width:'min(360px, calc(100vw - 40px))',maxHeight:'75vh',overflowY:'auto',padding:18,background:'#fff',border:'1px solid #ddd',borderRadius:18,boxShadow:'0 12px 50px #0002',marginBottom:10}},
      h('div',{style:{display:'flex',justifyContent:'space-between',alignItems:'center'}},h('strong',null,'Jase Tunnel'),h('button',{type:'button',style:button,onClick:()=>setOpen(false),'aria-label':'Chiudi pannello'},'×')),
      h('p',{role:'status'},message),
      !session?.connected&&h('div',null,
        h('p',null,'Apri Jase Tunnel sul computer, poi premi Collega. Il browser potrebbe chiedere l’accesso al servizio locale.'),
        h('button',{type:'button',style:button,disabled:busy,onClick:()=>act(async()=>{await client.current.discover();setSession(await client.current.connect());setMessage('Computer collegato.');})},busy?'Collegamento…':'Collega il mio modello'),
        detected&&h('p',null,h('a',{href:path+'download?target='+encodeURIComponent(detected.id)},'Scarica per '+detected.label)),
        h('details',null,h('summary',null,detected?'Altri sistemi operativi':'Scarica Jase Tunnel'),...downloads.map(d=>h('p',{key:d.id},h('a',{href:path+'download?target='+encodeURIComponent(d.id)},d.label)))),
        release?.message&&h('p',null,release.message),
        !downloads.length&&h('a',{href:'https://github.com/Strnozz/jase-tunnel/releases',target:'_blank',rel:'noopener noreferrer'},'Download e versioni su GitHub')),
      session?.connected&&h('div',null,h('p',null,model?`Modello: ${model.label}`:'Modello non pronto. Apri Ollama o LM Studio e ricollega il tunnel.'),
        ...history.map((item,i)=>h('p',{key:i,style:{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}},h('strong',null,item.role+': '),item.text)),
        pending&&h('div',null,h('p',null,'Consenti queste chiamate agli strumenti del servizio?'),h('pre',{style:{whiteSpace:'pre-wrap',fontSize:12}},JSON.stringify(pending.calls,null,2)),...[[true,'Consenti'],[false,'Rifiuta']].map(([allow,label])=>h('button',{key:label,type:'button',style:button,disabled:busy,onClick:()=>act(async()=>answer(await client.current.confirm(pending.id,allow)))},label))),
        h('form',{onSubmit:event=>{event.preventDefault();if(!input.trim()||!model||pending)return;const text=input;setInput('');setHistory(old=>[...old,{role:'Tu',text}]);void act(async()=>answer(await client.current.chat(model.id,text)));}},
          h('label',null,'Chiedi al tuo modello',h('textarea',{value:input,onChange:event=>setInput(event.target.value),disabled:busy||!!pending,maxLength:16000,style:{display:'block',width:'100%',boxSizing:'border-box',minHeight:70,marginTop:6,font:'inherit',border:'1px solid #ddd',borderRadius:8,padding:8}})),
          h('button',{type:'submit',style:button,disabled:busy||!model||!!pending},busy?'Risposta in corso…':'Invia')),
        h('button',{type:'button',style:{...button,marginTop:10},disabled:busy,onClick:()=>act(async()=>{await client.current.disconnect();setSession(null);setPending(null);setMessage('Computer scollegato.');})},'Scollega'))),
    h('button',{type:'button',style:{...button,boxShadow:'0 3px 15px #0002'},'aria-expanded':open,onClick:()=>setOpen(!open)},'Jase Tunnel'));
}
