import test from 'node:test'
import assert from 'node:assert/strict'
import * as runtime from './background-runtime.js'
function event() { const callbacks=[]; return { addListener(fn) { callbacks.push(fn) }, fire(...args) { for(const fn of callbacks) fn(...args) }, callbacks } }
function storage() { const values={}; return { values, async get(k) {return {[k]:structuredClone(values[k])}}, async set(v){Object.assign(values,structuredClone(v))},async remove(k){delete values[k]},async setAccessLevel(){} } }
const user='11111111-2222-4333-8444-555555555555'
function browser(){return {
 runtime:{id:'extension-id',onMessage:event(),onStartup:event(),onInstalled:event()},
 tabs:{onActivated:event(),onUpdated:event(),onRemoved:event(),query:async()=>[{id:1,url:'https://meet.google.com/abc-defg-hij'}]},
 windows:{onFocusChanged:event(),getLastFocused:async()=>({id:2,focused:true})},
 storage:{local:storage(),session:storage(),onChanged:event()},
 alarms:{onAlarm:event(),create:async()=>{}},
}}
test('background detects without popup and auth messages never expose tokens',async()=>{
 const chrome=browser();const calls=[]
 chrome.storage.session.values.auth_session={access_token:'private',refresh_token:'secret',expires_at:Date.now()/1000+3600}
 const worker=runtime.startBackground?.(chrome,{baseUrl:'https://api.test',auth:{restore:async()=>({user:{id:user},profile:{display_name:'A'}})},fetchImpl:async(url,options)=>{
  calls.push([url,JSON.parse(options.body)])
  return Response.json({meeting:{id:'aaaaaaaa-2222-4333-8444-555555555555',user_id:user,provider:'google_meet',provider_host:'meet.google.com',provider_room_id:'abc-defg-hij',meeting_key:'google_meet:meet.google.com:abc-defg-hij',canonical_url:'https://meet.google.com/abc-defg-hij'}})
 }})
 assert.ok(worker,'background runtime exists')
 assert.equal(chrome.tabs.onActivated.callbacks.length,1)
 await worker.scan()
 assert.equal(chrome.storage.session.values.meeting_status.status,'registered')
 assert.equal(calls.length,1)
 const listener=chrome.runtime.onMessage.callbacks[0]
 const message=await new Promise(resolve=>listener({type:'auth.restore'},{id:'extension-id'},resolve))
 assert.equal(message.ok,true)
 assert.ok(!JSON.stringify(message).includes('private'))
 assert.equal(listener({type:'auth.restore'},{id:'extension-id',tab:{id:3}},()=>{throw Error('untrusted sender')}),false)
 chrome.windows.getLastFocused=async()=>({id:2,focused:false})
 await worker.scan()
 assert.equal(chrome.storage.session.values.meeting_status.status,'no_active_tab')
})
test('login after browser restart preserves and retries the same account outbox',async()=>{
 const chrome=browser()
 chrome.tabs.query=async()=>[{id:2,url:'https://example.com/'}]
 chrome.storage.local.values.meeting_store={user_id:user,meetings:{},pending:{'google_meet:meet.google.com:abc-defg-hij':{canonical_url:'https://meet.google.com/abc-defg-hij',attempts:1,next_at:0}}}
 let calls=0
 const auth={login:async()=>{await chrome.storage.session.set({auth_session:{access_token:'private',expires_at:Date.now()/1000+3600}});return {user:{id:user},profile:{display_name:'A'}}}}
 const worker=runtime.startBackground(chrome,{auth,fetchImpl:async()=>{calls++;return Response.json({meeting:{id:'aaaaaaaa-2222-4333-8444-555555555555',user_id:user,provider:'google_meet',provider_host:'meet.google.com',provider_room_id:'abc-defg-hij',meeting_key:'google_meet:meet.google.com:abc-defg-hij',canonical_url:'https://meet.google.com/abc-defg-hij'}})}})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const result=await new Promise(resolve=>listener({type:'auth.login',input:{}},{id:'extension-id'},resolve))
 assert.equal(result.ok,true)
 await worker.retry()
 assert.equal(calls,1)
 assert.equal(Object.keys(chrome.storage.local.values.meeting_store.pending).length,0)
})
test('logout removes outbox recreated by an interleaved tab event',async()=>{
 const chrome=browser()
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 let release, started
 const ready=new Promise(resolve=>{started=resolve})
 const originalRemove=chrome.storage.session.remove.bind(chrome.storage.session)
 let held=false
 chrome.storage.session.remove=async key=>{
  if(key==='meeting_account' && !held){held=true;started();await new Promise(resolve=>{release=resolve})}
  await originalRemove(key)
 }
 const worker=runtime.startBackground(chrome,{auth:{logout:async()=>{await originalRemove('auth_session')}},fetchImpl:async()=>{throw Error('offline')}})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const logout=new Promise(resolve=>listener({type:'auth.logout'},{id:'extension-id'},resolve))
 await ready
 await worker.scan()
 release();const result=await logout
 assert.equal(result.ok,true)
 assert.equal(chrome.storage.local.values.meeting_store,undefined)
})
test('capture starts only from a registered meeting and the worker chooses it',async()=>{
 const chrome=browser()
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 const sent=[]
 chrome.tabCapture={getMediaStreamId:async({targetTabId})=>`stream-${targetTabId}`}
 chrome.offscreen={hasDocument:async()=>true,createDocument:async()=>{},closeDocument:async()=>{}}
 chrome.runtime.sendMessage=async message=>{sent.push(message);return message.type==='audio.capture.probe'?{ok:true,sample_rate:48000}:{ok:true}}
 const worker=runtime.startBackground(chrome,{baseUrl:'https://api.test',fetchImpl:async url=>{
  if(url.endsWith('/api/audio/runs')) return Response.json({run:{id:'bbbbbbbb-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',started_at:'2026-10-21T00:00:00Z'}})
  return Response.json({meeting:{id:'aaaaaaaa-2222-4333-8444-555555555555',user_id:user,provider:'google_meet',provider_host:'meet.google.com',provider_room_id:'abc-defg-hij',meeting_key:'google_meet:meet.google.com:abc-defg-hij',canonical_url:'https://meet.google.com/abc-defg-hij'}})
 }})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const result=await new Promise(resolve=>listener({type:'audio.start'},{id:'extension-id'},resolve))
 assert.equal(result.ok,true)
 assert.equal(chrome.storage.session.values.audio_status.status,'capturing')
 assert.equal(chrome.storage.session.values.audio_status.meeting_id,'aaaaaaaa-2222-4333-8444-555555555555')
 const start=sent.find(message=>message.type==='audio.capture.start')
 assert.equal(start.input.run_id,'bbbbbbbb-2222-4333-8444-555555555555')
 assert.equal(start.input.stream_id,'stream-1')
})
test('capture is refused when no meeting is registered',async()=>{
 const chrome=browser()
 chrome.tabs.query=async()=>[{id:1,url:'https://example.com/'}]
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 chrome.runtime.sendMessage=async()=>({ok:true})
 const worker=runtime.startBackground(chrome,{baseUrl:'https://api.test',fetchImpl:async()=>{throw Error('no request allowed')}})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const result=await new Promise(resolve=>listener({type:'audio.start'},{id:'extension-id'},resolve))
 assert.equal(result.ok,false)
 assert.match(result.error.message,/Belum ada meeting/)
})
test('a failed transcription stops the capture in the worker',async()=>{
 const chrome=browser()
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 const sent=[]
 chrome.tabCapture={getMediaStreamId:async({targetTabId})=>`stream-${targetTabId}`}
 chrome.offscreen={hasDocument:async()=>true,createDocument:async()=>{},closeDocument:async()=>{}}
 chrome.runtime.sendMessage=async message=>{sent.push(message);return message.type==='audio.capture.probe'?{ok:true,sample_rate:48000}:{ok:true}}
 const worker=runtime.startBackground(chrome,{baseUrl:'https://api.test',fetchImpl:async url=>{
  if(url.endsWith('/api/audio/runs')) return Response.json({run:{id:'bbbbbbbb-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',started_at:'2026-10-21T00:00:00Z'}})
  return Response.json({meeting:{id:'aaaaaaaa-2222-4333-8444-555555555555',user_id:user,provider:'google_meet',provider_host:'meet.google.com',provider_room_id:'abc-defg-hij',meeting_key:'google_meet:meet.google.com:abc-defg-hij',canonical_url:'https://meet.google.com/abc-defg-hij'}})
 }})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const sender={id:'extension-id',url:'chrome-extension://id/offscreen.html'}
 await new Promise(resolve=>listener({type:'audio.start'},{id:'extension-id'},resolve))
 const result=await new Promise(resolve=>listener({type:'audio.error',input:{message:'Transkripsi gagal.',source:'transcript'}},sender,resolve))
 assert.equal(result.ok,true)
 assert.equal(chrome.storage.session.values.audio_status.status,'stopped')
})
