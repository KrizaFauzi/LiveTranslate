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
 const requested=[]
 const worker=runtime.startBackground(chrome,{baseUrl:'https://api.test',fetchImpl:async (url,options)=>{
  requested.push([url,options])
  if(url.endsWith('/api/occurrences/connect')) return Response.json({occurrence:{id:'abababab-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',status:'pending_documents'}})
  if(url.endsWith('/api/occurrences/abababab-2222-4333-8444-555555555555/activate')) return Response.json({occurrence:{id:'abababab-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',status:'active'}})
  if(url.endsWith('/api/occurrences/abababab-2222-4333-8444-555555555555/finish')) return Response.json({occurrence:{id:'abababab-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',status:'awaiting_decision'}})
  if(url.endsWith('/api/occurrences/abababab-2222-4333-8444-555555555555/archive')) return Response.json({archive:{id:'cccccccc-2222-4333-8444-555555555555',occurrence_id:'abababab-2222-4333-8444-555555555555',status:'committed'}})
  if(url.includes('/api/documents?')) return Response.json({documents:[{id:'doc-1',file_name:'brief.csv',status:'ready'}]})
  if(url.endsWith('/api/documents')) return Response.json({document:{id:'doc-1',file_name:'brief.csv',status:'ready'},chunks:1},{status:201})
  if(url.includes('/api/questions?')) return Response.json({questions:[{id:'question-1',text:'When?'}]})
  if(url.includes('/api/archives?')) return Response.json({archives:[{id:'cccccccc-2222-4333-8444-555555555555'}],next_cursor:'next-page'})
  if(url.endsWith('/api/archives/cccccccc-2222-4333-8444-555555555555')) return Response.json({archive:{id:'cccccccc-2222-4333-8444-555555555555',manifest:{schema_version:2}},documents:[]})
  if(url.endsWith('/api/archives/cccccccc-2222-4333-8444-555555555555/export')) return new Response(new Uint8Array([80,75]),{headers:{'Content-Type':'application/zip'}})
  if(url.endsWith('/api/audio/runs/bbbbbbbb-2222-4333-8444-555555555555/translations')) return Response.json({translations:[{source_seq:1,status:'success',language:'id',text:'Halo'}]})
  if(url.endsWith('/api/audio/runs')) return Response.json({run:{id:'bbbbbbbb-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',occurrence_id:'abababab-2222-4333-8444-555555555555',started_at:'2026-10-21T00:00:00Z'}})
  return Response.json({meeting:{id:'aaaaaaaa-2222-4333-8444-555555555555',user_id:user,provider:'google_meet',provider_host:'meet.google.com',provider_room_id:'abc-defg-hij',meeting_key:'google_meet:meet.google.com:abc-defg-hij',canonical_url:'https://meet.google.com/abc-defg-hij'}})
 }})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const connected=await new Promise(resolve=>listener({type:'occurrence.connect'},{id:'extension-id'},resolve))
 assert.equal(connected.ok,true)
 const result=await new Promise(resolve=>listener({type:'audio.start'},{id:'extension-id'},resolve))
 assert.equal(result.ok,true)
 assert.equal(chrome.storage.session.values.audio_status.status,'capturing')
 assert.equal(chrome.storage.session.values.audio_status.meeting_id,'aaaaaaaa-2222-4333-8444-555555555555')
 assert.equal(chrome.storage.session.values.audio_status.occurrence_id,'abababab-2222-4333-8444-555555555555')
 assert.equal(requested.filter(([url])=>url.endsWith('/api/occurrences/connect')).length,1)
 const start=sent.find(message=>message.type==='audio.capture.start')
 assert.equal(start.input.run_id,'bbbbbbbb-2222-4333-8444-555555555555')
 assert.equal(start.input.stream_id,'stream-1')
 const translated=await new Promise(resolve=>listener({type:'translation.current.read'},{id:'extension-id'},resolve))
 assert.equal(translated.ok,true)
 assert.equal(translated.data.translations[0].text,'Halo')
 const uploaded=await new Promise(resolve=>listener({type:'document.upload',input:{name:'brief.csv',type:'text/csv',bytes:new TextEncoder().encode('a,b').buffer}},{id:'extension-id'},resolve))
 assert.equal(uploaded.ok,true)
 const listed=await new Promise(resolve=>listener({type:'documents.list'},{id:'extension-id'},resolve))
 assert.equal(listed.data.documents[0].status,'ready')
 const questions=await new Promise(resolve=>listener({type:'questions.current.read'},{id:'extension-id'},resolve))
 assert.equal(questions.data.questions[0].text,'When?')
 const stopped=await new Promise(resolve=>listener({type:'audio.stop'},{id:'extension-id'},resolve))
 assert.equal(stopped.ok,true)
 assert.equal(chrome.storage.session.values.meeting_occurrence.status,'awaiting_decision')
 const archived=await new Promise(resolve=>listener({type:'occurrence.archive'},{id:'extension-id'},resolve))
 assert.equal(archived.ok,true)
 assert.equal(chrome.storage.session.values.meeting_occurrence.status,'archived')
 const archives=await new Promise(resolve=>listener({type:'archives.list'},{id:'extension-id'},resolve))
 assert.equal(archives.data.archives.length,1)
 const nextArchives=await new Promise(resolve=>listener({type:'archives.list',input:{cursor:'next-page'}},{id:'extension-id'},resolve))
 assert.equal(nextArchives.ok,true)
 assert.ok(requested.some(([url])=>url.includes('cursor=next-page')))
 const detail=await new Promise(resolve=>listener({type:'archive.detail',input:{id:'cccccccc-2222-4333-8444-555555555555'}},{id:'extension-id'},resolve))
 assert.equal(detail.data.archive.manifest.schema_version,2)
 const exported=await new Promise(resolve=>listener({type:'archive.export',input:{id:'cccccccc-2222-4333-8444-555555555555'}},{id:'extension-id'},resolve))
 assert.equal(exported.data.bytes.byteLength,2)
})
test('connection offer dismissal creates no occurrence and is stored for the current visit',async()=>{
 const chrome=browser()
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 let connects=0
 runtime.startBackground(chrome,{baseUrl:'https://api.test',fetchImpl:async url=>{
  if(url.endsWith('/api/occurrences/connect')){connects++;return Response.json({})}
  return Response.json({meeting:{id:'aaaaaaaa-2222-4333-8444-555555555555',user_id:user,provider:'google_meet',provider_host:'meet.google.com',provider_room_id:'abc-defg-hij',meeting_key:'google_meet:meet.google.com:abc-defg-hij',canonical_url:'https://meet.google.com/abc-defg-hij'}})
 }})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const dismissed=await new Promise(resolve=>listener({type:'meeting.offer.dismiss'},{id:'extension-id'},resolve))
 assert.equal(dismissed.ok,true)
 assert.equal(dismissed.data.offer_dismissed,true)
 assert.equal(connects,0)
 assert.equal(chrome.storage.session.values.connection_offer_dismissed.canonical_url,'https://meet.google.com/abc-defg-hij')
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
  if(url.endsWith('/api/occurrences/connect')) return Response.json({occurrence:{id:'abababab-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',status:'pending_documents'}})
  if(url.endsWith('/api/occurrences/abababab-2222-4333-8444-555555555555/activate')) return Response.json({occurrence:{id:'abababab-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',status:'active'}})
  if(url.endsWith('/api/occurrences/abababab-2222-4333-8444-555555555555/finish')) return Response.json({occurrence:{id:'abababab-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',status:'awaiting_decision'}})
  if(url.endsWith('/api/audio/runs')) return Response.json({run:{id:'bbbbbbbb-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',occurrence_id:'abababab-2222-4333-8444-555555555555',started_at:'2026-10-21T00:00:00Z'}})
  return Response.json({meeting:{id:'aaaaaaaa-2222-4333-8444-555555555555',user_id:user,provider:'google_meet',provider_host:'meet.google.com',provider_room_id:'abc-defg-hij',meeting_key:'google_meet:meet.google.com:abc-defg-hij',canonical_url:'https://meet.google.com/abc-defg-hij'}})
 }})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const sender={id:'extension-id',url:'chrome-extension://id/offscreen.html'}
 await new Promise(resolve=>listener({type:'occurrence.connect'},{id:'extension-id'},resolve))
 await new Promise(resolve=>listener({type:'audio.start'},{id:'extension-id'},resolve))
 const result=await new Promise(resolve=>listener({type:'audio.error',input:{owner_id:user,run_id:'bbbbbbbb-2222-4333-8444-555555555555',message:'Transkripsi gagal.',source:'transcript'}},sender,resolve))
 assert.equal(result.ok,true)
 assert.equal(chrome.storage.session.values.audio_status.status,'stopped')
})

test('logout flushes the owning run before token removal and clears all transient text', async () => {
 const chrome=browser(), order=[]
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 chrome.storage.session.values.audio_status={owner_id:user,run_id:'bbbbbbbb-2222-4333-8444-555555555555',status:'capturing'}
 chrome.storage.session.values.transcript_state={owner_id:user,run_id:'bbbbbbbb-2222-4333-8444-555555555555',status:'transcribed',finals:[{text:'private final'}]}
 chrome.storage.session.values.meeting_occurrence={id:'cccccccc-2222-4333-8444-555555555555',meeting_id:'aaaaaaaa-2222-4333-8444-555555555555',status:'active'}
 chrome.offscreen={closeDocument:async()=>{order.push('closed')}}
 chrome.runtime.sendMessage=async message=>{if(message.type==='audio.capture.flush')order.push('flush');return {ok:true}}
 runtime.startBackground(chrome,{auth:{logout:async()=>{order.push('logout');await chrome.storage.session.remove('auth_session')}},fetchImpl:async url=>{
  if(url.endsWith('/stop')){order.push('stop');assert.equal(chrome.storage.session.values.auth_session.access_token,'private');return Response.json({run:{}})}
  if(url.endsWith('/finish')){order.push('finish');assert.equal(chrome.storage.session.values.auth_session.access_token,'private');return Response.json({occurrence:{id:'cccccccc-2222-4333-8444-555555555555',status:'awaiting_decision'}})}
  throw Error('offline')
 }})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const result=await new Promise(resolve=>listener({type:'auth.logout'},{id:'extension-id'},resolve))
 assert.equal(result.ok,true)
 assert.deepEqual(order,['flush','stop','closed','finish','logout'])
 assert.equal(chrome.storage.session.values.transcript_state,undefined)
 assert.equal(chrome.storage.session.values.audio_status,undefined)
 assert.equal(chrome.storage.session.values.meeting_occurrence,undefined)
 const sender={id:'extension-id',url:'chrome-extension://id/offscreen.html'}
 const token=await new Promise(resolve=>listener({type:'audio.token',input:{owner_id:user,run_id:'bbbbbbbb-2222-4333-8444-555555555555'}},sender,resolve))
 assert.equal(token.data.token,null)
})

test('saved RPCs use worker credentials, constrain meeting and reject tab senders', async () => {
 const chrome=browser(), requests=[]
 const meetingId='aaaaaaaa-2222-4333-8444-555555555555', runId='bbbbbbbb-2222-4333-8444-555555555555'
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 chrome.storage.session.values.meeting_status={status:'registered',user_id:user,meeting_id:meetingId}
 runtime.startBackground(chrome,{baseUrl:'https://api.test',fetchImpl:async(url,options)=>{
  requests.push([url,options])
  if(url.includes('/segments')) return Response.json({run:{id:runId,meeting_id:meetingId},storage:{state:'saved',complete:true},segments:[],through_seq:0,next_after_seq:null})
  return Response.json({runs:[{id:runId,meeting_id:meetingId}],next_cursor:null})
 }})
 const listener=chrome.runtime.onMessage.callbacks[0], sender={id:'extension-id'}
 const list=await new Promise(resolve=>listener({type:'transcript.runs.list',input:{meeting_id:meetingId}},sender,resolve))
 const read=await new Promise(resolve=>listener({type:'transcript.run.read',input:{meeting_id:meetingId,run_id:runId,through_seq:0}},sender,resolve))
 assert.equal(list.ok,true);assert.equal(read.data.through_seq,0)
 assert.equal(requests[0][1].headers.Authorization,'Bearer private')
 assert.ok(!JSON.stringify([list,read]).includes('private'))
 assert.equal(listener({type:'transcript.run.read',input:{meeting_id:meetingId,run_id:runId}},{...sender,tab:{id:5}},()=>{}),false)
 const foreign=await new Promise(resolve=>listener({type:'transcript.runs.list',input:{meeting_id:'cccccccc-2222-4333-8444-555555555555'}},sender,resolve))
 assert.equal(foreign.ok,false);assert.equal(requests.length,2)
})

test('audio.start rejects the superseded pre-capture archive option before creating a run', async () => {
 const chrome=browser()
 runtime.startBackground(chrome,{fetchImpl:async()=>{throw Error('no request allowed')}})
 const listener=chrome.runtime.onMessage.callbacks[0]
 for(const save_transcript of [null,'true',1,false,true]){
  const result=await new Promise(resolve=>listener({type:'audio.start',input:{save_transcript}},{id:'extension-id'},resolve))
  assert.equal(result.ok,false)
  assert.match(result.error.message,/tidak menerima pilihan arsip/)
 }
})

test('an account transition removes owner-bound live text even without a surviving capture state', async () => {
 const chrome=browser()
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 chrome.storage.session.values.transcript_state={owner_id:'previous-owner',run_id:'old-run',finals:[{text:'previous private words'}]}
 runtime.startBackground(chrome,{auth:{restore:async()=>({user:{id:user},profile:{display_name:'A'}})},fetchImpl:async()=>{throw Error('offline')}})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const result=await new Promise(resolve=>listener({type:'auth.restore'},{id:'extension-id'},resolve))
 assert.equal(result.ok,true)
 assert.equal(chrome.storage.session.values.transcript_state,undefined)
})

test('logout aborts on a retryable backend stop failure and preserves owner state', async () => {
 const chrome=browser(), calls=[]
 chrome.storage.session.values.auth_session={access_token:'private',expires_at:Date.now()/1000+3600}
 chrome.storage.session.values.meeting_account={id:user}
 chrome.storage.session.values.audio_status={owner_id:user,run_id:'bbbbbbbb-2222-4333-8444-555555555555',status:'capturing'}
 chrome.storage.session.values.meeting_occurrence={id:'cccccccc-2222-4333-8444-555555555555',status:'active'}
 chrome.runtime.sendMessage=async()=>({ok:true})
 runtime.startBackground(chrome,{auth:{logout:async()=>{calls.push('logout');await chrome.storage.session.remove('auth_session')}},fetchImpl:async url=>{
  calls.push(url)
  if(url.endsWith('/stop'))return Response.json({error:{message:'stop unavailable'}},{status:503})
  if(url.endsWith('/finish'))return Response.json({occurrence:{id:'cccccccc-2222-4333-8444-555555555555',status:'awaiting_decision'}})
  throw Error('unexpected request')
 }})
 const listener=chrome.runtime.onMessage.callbacks[0]
 const result=await new Promise(resolve=>listener({type:'auth.logout'},{id:'extension-id'},resolve))
 assert.equal(result.ok,false)
 assert.equal(chrome.storage.session.values.auth_session.access_token,'private')
 assert.equal(chrome.storage.session.values.audio_status.status,'stop_failed')
 assert.ok(!calls.includes('logout'))
 assert.ok(!calls.some(value=>String(value).endsWith('/finish')))
})

test('logout survives revoked refresh, clears tokens and text, and releases the auth transition for login', async () => {
 const chrome=browser(), order=[]
 chrome.storage.session.values.auth_session={access_token:'revoked',expires_at:0}
 chrome.storage.session.values.meeting_account={id:user}
 chrome.storage.session.values.audio_status={owner_id:user,run_id:'bbbbbbbb-2222-4333-8444-555555555555',status:'capturing'}
 chrome.storage.session.values.transcript_state={owner_id:user,run_id:'bbbbbbbb-2222-4333-8444-555555555555',status:'transcribing',finals:[{text:'private final'}]}
 chrome.offscreen={closeDocument:async()=>{order.push('closed')}}
 chrome.runtime.sendMessage=async()=>({ok:true})
 runtime.startBackground(chrome,{auth:{
  restore:async()=>{throw Object.assign(new Error('revoked refresh'),{status:401})},
  logout:async()=>{order.push('logout');await chrome.storage.session.remove('auth_session')},
  login:async()=>{await chrome.storage.session.set({auth_session:{access_token:'fresh',expires_at:Date.now()/1000+3600}});return {user:{id:user},profile:{display_name:'A'}}},
 },fetchImpl:async()=>{throw Error('offline')}})
 const listener=chrome.runtime.onMessage.callbacks[0], sender={id:'extension-id'}
 const logout=await new Promise(resolve=>listener({type:'auth.logout'},sender,resolve))
 assert.equal(logout.ok,true)
 assert.deepEqual(order,['closed','logout'])
 assert.equal(chrome.storage.session.values.auth_session,undefined)
 assert.equal(chrome.storage.session.values.transcript_state,undefined)
 assert.equal(chrome.storage.session.values.audio_status,undefined)
 const login=await new Promise(resolve=>listener({type:'auth.login',input:{}},sender,resolve))
 assert.equal(login.ok,true)
 const inspected=await new Promise(resolve=>listener({type:'meeting.inspect'},sender,resolve))
 assert.equal(inspected.ok,true,'authChanging cannot remain stuck after stop credential failure')
 assert.equal(chrome.storage.session.values.auth_session.access_token,'fresh')
})
