/* Observer renders API values as text, never as HTML. */
const app = document.getElementById('app');
const statusNode = document.getElementById('live-status');
const dateTime = new Intl.DateTimeFormat('zh-TW', {dateStyle: 'short', timeStyle: 'medium'});
const number = new Intl.NumberFormat('zh-TW');
const labels = {domain:'域名', ip_only:'僅 IP', destination_ip:'目標 IP', asn:'ASN', proxy_path:'代理路徑'};
const problemLabels = {repeated_connections:'重複連線跡象', small_long_connections:'小流量長連線補充證據', ipv6_observation_difference:'值得檢查 IPv6 路徑'};
let renderToken = 0;
let flowTimer;

function node(tag, className, content) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (content !== undefined && content !== null) element.textContent = String(content);
  return element;
}
function put(parent, ...children) { children.forEach(child => parent.append(child)); return parent; }
function link(label, href) { const a = node('a', '', label); a.href = href; return a; }
function detailURL(kind, value) { return `#/detail/${encodeURIComponent(kind)}/${encodeURIComponent(value)}`; }
function when(ms) { return ms ? dateTime.format(new Date(ms)) : '未知'; }
function bytes(n) {
  if (n === undefined || n === null) return '未知';
  const units = ['B','KiB','MiB','GiB','TiB']; let v = Number(n), i = 0;
  while (v >= 1024 && i < units.length-1) { v /= 1024; i++; }
  return `${number.format(i ? Math.round(v*10)/10 : v)} ${units[i]}`;
}
function textOrUnknown(value) { return value || '未知'; }
function title(eyebrow, heading, intro, time) {
  const head = node('div','page-head'); const lead = node('div');
  put(lead,node('p','eyebrow',eyebrow),node('h1','',heading),node('p','',intro));
  put(head,lead,node('span','time',time)); return head;
}
function panel(name, action) {
  const section = node('section','panel'); const head = node('div','panel-head');
  put(head,node('h2','',name)); if (action) head.append(action); section.append(head); return section;
}
function row(main, meta, value, href) {
  const item = node('div','row'), left = node('div','row-main');
  put(left,href ? link(main,href) : node('strong','',main),node('div','row-meta',meta));
  put(item,left,node('div','row-value',value)); return item;
}
function empty(message) { return node('p','empty',message); }
function savedSort(key, fallback) {
  try { const v = JSON.parse(localStorage.getItem(`observer.sort.${key}`)); return v && typeof v.field === 'string' && typeof v.desc === 'boolean' ? v : fallback; }
  catch { return fallback; }
}
function saveSort(key, value) { try { localStorage.setItem(`observer.sort.${key}`, JSON.stringify(value)); } catch {} }
function sortControls(key, fields, fallback, onChange) {
  const state = savedSort(key, fallback);
  if (!fields.some(f => f[0] === state.field)) Object.assign(state,fallback);
  const wrap = node('div','sort-controls');
  const label = node('label','sort-label','排序');
  const select = node('select','sort-select'); select.setAttribute('aria-label',`${key}排序欄位`);
  fields.forEach(([field,name]) => { const option = node('option','',name); option.value=field; select.append(option); });
  select.value=state.field;
  const direction=node('button','sort-direction',state.desc ? '降序 ↓' : '升序 ↑'); direction.type='button';
  const reset=node('button','sort-reset','預設'); reset.type='button';
  function change() { saveSort(key,state); direction.textContent=state.desc ? '降序 ↓' : '升序 ↑'; onChange(state); }
  select.addEventListener('change',()=>{state.field=select.value;change();});
  direction.addEventListener('click',()=>{state.desc=!state.desc;change();});
  reset.addEventListener('click',()=>{Object.assign(state,fallback);select.value=state.field;change();});
  put(label,select); put(wrap,label,direction,reset); return {element:wrap,state};
}
function compareValue(a,b) {
  if (a === b) return 0;
  if (a === null || a === undefined) return 1;
  if (b === null || b === undefined) return -1;
  if (typeof a === 'number' && typeof b === 'number') return a-b;
  return String(a).localeCompare(String(b),'zh-Hant',{numeric:true,sensitivity:'base'});
}
function sortedPanel(section,key,items,fields,fallback,renderItem,emptyText,limitNote,pageSize=0) {
  const body=node('div','sort-body'), pager=node('div','pager'); let page=1;
  const {element,state}=sortControls(key,fields,fallback,()=>{page=1;draw();});
  section.querySelector('.panel-head').append(element);
  if (limitNote) section.append(node('p','row-meta',limitNote));
  section.append(body); if (pageSize) section.append(pager);
  function draw() {
    body.replaceChildren(); pager.replaceChildren();
    if (!items?.length) { body.append(empty(emptyText)); return; }
    const ordered=[...items].sort((a,b)=>{
      const field=fields.find(f=>f[0]===state.field);
      const result=compareValue(field[2](a),field[2](b));
      return (state.desc ? -result : result) || compareValue(JSON.stringify(a),JSON.stringify(b));
    });
    const pages=pageSize ? Math.ceil(ordered.length/pageSize) : 1;
    page=Math.min(page,pages);
    (pageSize ? ordered.slice((page-1)*pageSize,page*pageSize) : ordered).forEach(item=>body.append(renderItem(item)));
    if (pages>1) {
      const prev=node('button','filter','上一頁'),next=node('button','filter','下一頁');
      prev.type=next.type='button';prev.disabled=page===1;next.disabled=page===pages;
      prev.addEventListener('click',()=>{page--;draw();});next.addEventListener('click',()=>{page++;draw();});
      put(pager,prev,node('span','row-meta',`第 ${page} / ${pages} 頁 · 共 ${number.format(ordered.length)} 條路徑`),next);
    }
  }
  body.redraw=draw; draw(); return body;
}
function sortableTable(section,key,items,fields,fallback,cells) {
  const wrap=node('div','table-wrap'), table=node('table'), head=node('tr'), body=node('tbody');
  fields.forEach(f=>head.append(node('th','',f[1])));
  put(table,put(node('thead'),head),body); wrap.append(table); section.append(wrap);
  const {element,state}=sortControls(key,fields,fallback,draw); section.querySelector('.panel-head').append(element);
  function draw() {
    body.replaceChildren();
    [...items].sort((a,b)=>{
      const f=fields.find(x=>x[0]===state.field);
      const c=compareValue(f[2](a),f[2](b));
      return (state.desc?-c:c) || compareValue(JSON.stringify(a),JSON.stringify(b));
    }).forEach(item=>{const tr=node('tr'); cells(item).forEach(v=>tr.append(node('td','',v)));body.append(tr);});
  }
  draw(); return wrap;
}
async function pagedPanel(section,key,endpoint,fields,fallback,renderItem,emptyText) {
  const body=node('div','sort-body'), pager=node('div','pager'); let page=1, request=0;
  const {element,state}=sortControls(key,fields,fallback,()=>{page=1;load();});
  section.querySelector('.panel-head').append(element); put(section,body,pager);
  async function load() {
    const current=++request;
    body.replaceChildren(node('p','loading','載入中…'));
    try {
      const params=new URLSearchParams({page:String(page),sort:state.field,dir:state.desc?'desc':'asc'});
      const data=await get(`${endpoint}?${params}`);
      if (current!==request || !section.isConnected) return;
      body.replaceChildren(); pager.replaceChildren();
      if (!data.items.length) body.append(empty(emptyText));
      data.items.forEach(item=>body.append(renderItem(item)));
      if (!data.total) return;
      const totalPages=Math.max(1,Math.ceil(data.total/data.limit));
      if (page>totalPages) {page=totalPages;load();return;}
      const prev=node('button','filter','上一頁'), next=node('button','filter','下一頁');
      prev.type=next.type='button'; prev.disabled=page<=1; next.disabled=page>=totalPages;
      prev.addEventListener('click',()=>{page--;load();}); next.addEventListener('click',()=>{page++;load();});
      put(pager,prev,node('span','row-meta',`第 ${page} / ${totalPages} 頁 · 共 ${number.format(data.total)} 筆`),next);
    } catch (error) { if (current===request) body.replaceChildren(empty(error.message)); }
  }
  // The section is attached by its caller before this asynchronous request resolves.
  load();
}
async function get(path) {
  const response = await fetch(new URL(path,location.origin),{cache:'no-store'});
  if (!response.ok) throw new Error(response.status === 401 ? '認證失敗，請重新登入。' : `資料讀取失敗（HTTP ${response.status}）。`);
  return response.json();
}
function showError(error) { app.replaceChildren(title('讀取失敗','暫時無法顯示觀測資料',error.message,'—')); }
function setNav(active) {
  document.querySelectorAll('[data-nav]').forEach(a => {
    if (a.dataset.nav === active) a.setAttribute('aria-current','page'); else a.removeAttribute('aria-current');
  });
}

async function dashboard(token) {
  setNav('dashboard');
  const [data, status] = await Promise.all([get('/api/dashboard'),get('/api/status')]);
  if (token !== renderToken) return;
  const last = data.last_connections_at_ms;
  const fresh = last && data.current_connections !== undefined;
  statusNode.textContent = !last ? '尚未採集' : fresh ? '資料已更新' : '採集資料已過期';
  const content = document.createDocumentFragment();
  content.append(title('OBSERVATION / 01','觀測總覽','本頁數字只計入 Observer 實際看見的連線與流量。',`資料時間 ${when(last)}`));
  if (!fresh) {
    const notice = node('div','notice');
    let reason = '';
    if (data.interruption_reason) {
      const code = data.interruption_reason;
      reason = code === 'invalid_snapshot' ? '快照格式無法解讀。' : code === 'mailbox_overflow' ? '採集佇列丟幀。' : code === 'writer_failed' ? '資料寫入失敗。' : code?.startsWith('controller status ') ? `Controller 回應 ${code.slice(18)}。` : 'Controller 連線或讀取失敗。';
      reason = `自 ${when(data.interruption_at_ms)} 起：${reason}`;
    }
    put(notice,node('strong','',last ? '目前連線數未知' : '尚未收到連線快照'),node('p','',last ? `最後成功採樣：${when(last)}。${reason}缺口期間的流量不補算。` : `等待 Controller 的第一個有效快照。${reason}`));
    content.append(notice);
  }
  const metrics = node('div','metrics');
  const values = [
    ['目前已觀測連線',fresh ? number.format(data.current_connections) : '未知', fresh ? '來自最近的完整快照' : '等待新快照'],
    ['今日已觀測連線',number.format(data.today_observed_connections),'按首次觀測時間計'],
    ['今日已觀測流量',bytes(data.today_observed_upload_bytes + data.today_observed_download_bytes),`上傳 ${bytes(data.today_observed_upload_bytes)} · 下載 ${bytes(data.today_observed_download_bytes)}`],
    ['近期問題證據',number.format(data.recent_problems),'最近 7 日']
  ];
  values.forEach(([label,value,note]) => { const box = node('div','metric'); put(box,node('div','metric-label',label),node('span','metric-value',value),node('div','metric-note',note)); metrics.append(box); });
  content.append(metrics);
  const columns = node('div','two-column');
  const gaps = panel('採集缺口');
  sortedPanel(gaps,'gaps',data.recent_gaps,[["start","開始時間",g=>g.started_at_ms],["end","結束時間",g=>g.ended_at_ms],["reason","原因",g=>g.reason],["drops","丟幀數",g=>g.dropped_samples]],{field:'start',desc:true},g=>row(when(g.started_at_ms),`${g.stream} · ${g.reason}${g.dropped_samples ? ` · 丟幀 ${g.dropped_samples}` : ''}`,g.ended_at_ms ? `至 ${when(g.ended_at_ms)}` : '仍在持續'),'最近 7 日沒有已記錄的缺口。');
  const guide = panel('下一步');
  put(guide,row('查看目標','按完整域名或目標 IP 查看已觀測路徑','→','#/targets'),row('閱讀問題證據','計數與檢查方向由實際快照推導','→','#/problems'));
  if (status.collector?.read_errors) guide.append(row('Controller 讀取錯誤','連線或憑據可能需要檢查',number.format(status.collector.read_errors)));
  put(columns,gaps,guide); content.append(columns); app.replaceChildren(content);
}

async function targets(token) {
  setNav('targets'); statusNode.textContent = '歷史目標';
  if (token !== renderToken) return;
  const content = document.createDocumentFragment();
  content.append(title('TARGETS / 02','已觀測目標','列出保留的日統計中存在的域名與 IP；不進行反向 DNS 猜測。','日統計歷史'));
  const section = panel('目標列表');
  content.append(section); app.replaceChildren(content);
  pagedPanel(section,'targets','/api/targets',[["last_seen","最後觀測"],["name","名稱"],["kind","類型"],["connections","連線數"],["bytes","流量"]],{field:'last_seen',desc:true},item=>row(item.value,`${labels[item.kind] || item.kind} · 最後觀測 ${when(item.last_seen_at_ms)}`,`日計數合計 ${number.format(item.observed_connections)} · ${bytes(item.observed_upload_bytes + item.observed_download_bytes)}`,detailURL(item.kind,item.value)),'目前沒有可顯示的目標；等待快照和首次投影。');
}

function evidenceDetails(problem) {
  const item = node('details','evidence'); const summary = node('summary');
  const lead = node('div','row-main');
  put(lead,node('h3','',problemLabels[problem.kind] || '值得檢查的觀測證據'),node('div','row-meta',`${problem.target} · ${when(problem.last_evidence_at_ms)}`));
  put(summary,lead,node('span','row-value',`${number.format(problem.sample_count)} 樣本`)); item.append(summary);
  const body = node('div','evidence-body');
  const route = `${textOrUnknown(problem.rule)}${problem.rule_payload ? ` (${problem.rule_payload})` : ''} · ${problem.chains_json || '[]'}`;
  const dl = node('dl');
  [['實際規則與鏈',route],['把握度',`${problem.confidence}/3`],['檢查方向',problem.kind === 'ipv6_observation_difference' ? '檢查 IPv6 可達性、DNS AAAA 與實際分流。' : '核對此目標的規則、代理鏈和目的位址。']].forEach(([k,v]) => put(dl,node('dt','',k),node('dd','',v)));
  for (const [key,value] of Object.entries(problem.evidence || {})) {
    if (key === 'interpretation') continue;
    put(dl,node('dt','',key.replaceAll('_',' ')),node('dd','',typeof value === 'object' ? JSON.stringify(value) : value));
  }
  put(body,dl,link('查看目標詳情 →',detailURL(problem.target_kind,problem.target))); item.append(body); return item;
}
function historyEntry(event) {
  const item = node('details','evidence'); const summary = node('summary');
  const lead = node('div','row-main');
  put(lead,node('h3','',problemLabels[event.kind] || event.kind),node('div','row-meta',`${when(event.window_start_ms)} · ${event.target} · 路徑 #${event.route_id}`));
  put(summary,lead,node('span','row-value',`${number.format(event.sample_count)} 樣本`)); item.append(summary);
  const body = node('div','evidence-body'), dl = node('dl');
  for (const [key,value] of Object.entries(event.evidence || {})) {
    if (key === 'interpretation') continue;
    put(dl,node('dt','',key.replaceAll('_',' ')),node('dd','',typeof value === 'object' ? JSON.stringify(value) : value));
  }
  put(body,dl,link('查看目標 →',detailURL(event.target_kind,event.target))); item.append(body);
  return item;
}
async function problems(token) {
  setNav('problems'); statusNode.textContent = '最近 7 日';
  if (token !== renderToken) return;
  const content = document.createDocumentFragment();
  content.append(title('EVIDENCE / 03','問題證據','這些模式提示檢查方向；它們不是請求失敗或分流錯誤的判定。','最近 7 日'));
  const section = panel('達到門檻的觀測模式');
  content.append(section); app.replaceChildren(content);
  pagedPanel(section,'problems','/api/problems',[["severity","嚴重程度"],["last_seen","最近證據"],["target","目標"],["kind","類型"],["confidence","把握度"],["samples","樣本數"]],{field:'severity',desc:true},evidenceDetails,'目前沒有達到展示門檻的問題證據。');
}

function trendRows(items, granularity, gaps) {
  const section = panel(`${granularity}趨勢`);
  const totals = new Map();
  items.filter(x => x.route_id === 0 || x.route_id === Number(window.location.hash.split('/').at(-1))).forEach(x => {
    const current = totals.get(x.bucket_start_ms) || {count:0,up:0,down:0};
    current.count += x.observed_connections; current.up += x.observed_upload_bytes; current.down += x.observed_download_bytes;
    totals.set(x.bucket_start_ms,current);
  });
  if (!totals.size) { section.append(empty('此時間範圍尚無聚合資料。')); return section; }
  const observed = [...totals.keys()].sort((a,b) => a-b);
  const gapBuckets = new Set();
  if (granularity === '小時') gaps.forEach(g => {
    const first = Math.max(Math.floor(g.started_at_ms/3600000)*3600000,observed[0]);
    const last = Math.min(Math.floor((g.ended_at_ms || g.started_at_ms)/3600000)*3600000,observed.at(-1));
    for (let bucket=first;bucket<=last;bucket+=3600000) gapBuckets.add(bucket);
  });
  gapBuckets.forEach(bucket => { if (bucket >= observed[0] && bucket <= observed.at(-1) && !totals.has(bucket)) totals.set(bucket,null); });
  const ordered = [...totals].sort((a,b) => a[0]-b[0]);
  const bars = node('div','trend'); bars.setAttribute('role','img'); bars.setAttribute('aria-label',`${granularity}已觀測連線柱狀圖；缺口以斜線標記，詳細數字見下方`);
  const max = Math.max(...ordered.map(([,x]) => x?.count || 0),1);
  ordered.forEach(([bucket,x]) => { const bar = node('div',gapBuckets.has(bucket) ? 'bar gap' : 'bar'); bar.style.height = x ? `${Math.max(4,Math.round(x.count/max*96))}px` : '96px'; bar.title = `${when(bucket)} · ${x ? `${x.count} 條已觀測連線${gapBuckets.has(bucket) ? '，同時有缺口' : ''}` : '採集缺口，數值未知'}`; bars.append(bar); });
  section.append(bars);
  if (gaps.length) {
    section.append(node('p','row-meta',`最近 30 日記錄 ${gaps.length} 個採集缺口；空白時段不可視為零流量。`));
    const gapSection=node('section','comparison-subpanel');gapSection.append(put(node('div','panel-head'),node('h3','','近期採集缺口')));
    sortedPanel(gapSection,'trend.gaps',gaps.slice(0,5),[["start","開始",g=>g.started_at_ms],["end","結束",g=>g.ended_at_ms],["reason","原因",g=>g.reason]],{field:'start',desc:true},g=>node('div','row-meta',`${when(g.started_at_ms)} 至 ${when(g.ended_at_ms)} · ${g.reason}`),'沒有缺口。','最多顯示 5 個近期缺口；排序僅作用於已載入結果。');
    section.append(gapSection);
  }
  const visible=[...ordered].reverse().slice(0,granularity === '每日' ? ordered.length : 80);
  if (granularity==='小時' && ordered.length>80) section.append(node('p','row-meta','表格顯示最近 80 個小時；排序僅作用於已載入結果。'));
  sortableTable(section,`trend.${granularity}`,visible,[["time","時間",r=>r[0]],["connections","連線",r=>r[1]?.count],["up","上傳",r=>r[1]?.up],["down","下載",r=>r[1]?.down]],{field:'time',desc:true},([bucket,x])=>[when(bucket),x ? number.format(x.count) : '未知（缺口）',x ? bytes(x.up) : '未知',x ? bytes(x.down) : '未知']);
  return section;
}
async function detail(token, kind, value) {
  setNav(''); statusNode.textContent = labels[kind] || kind;
  const data = await get(`/api/details/${encodeURIComponent(kind)}/${encodeURIComponent(value)}`);
  if (token !== renderToken) return;
  const content = document.createDocumentFragment();
  const heading = kind === 'proxy_path' ? textOrUnknown(data.paths?.[0]?.rule) : value;
  const intro = kind === 'proxy_path' ? `路徑 #${value} · ${data.paths?.[0]?.rule_payload || '無規則載荷'} · ${Array.isArray(data.paths?.[0]?.chains) && data.paths[0].chains.length ? data.paths[0].chains.join(' → ') : '代理鏈未知'}` : '歷史資料按實際觀測路徑歸組；缺失欄位保持未知。';
  content.append(title(`${labels[kind] || kind} / DETAIL`,heading,intro,`最近 30 日 · ${data.hourly.length} 條小時統計`));
  if (kind==='domain' || kind==='ip_only') content.append(link('在流向地圖查看此目標 →',`#/flows/${encodeURIComponent(value)}`));
  if (kind === 'domain') {
    const sources = panel('域名來源 · 原始資料保留期');
    sortedPanel(sources,`sources.${kind}`,data.observed_sources,[["source","來源",s=>s.source],["count","連線數",s=>s.observed_connections],["last","最近",s=>s.last_seen_at_ms]],{field:'source',desc:false},source=>row(source.source,`首次 ${when(source.first_seen_at_ms)} · 最近 ${when(source.last_seen_at_ms)}`,`${number.format(source.observed_connections)} 條`),'來源未知；原始記錄可能已過期。');
    content.append(sources);
  }
  const cols = node('div','two-column');
  const paths = panel('實際觀測路徑');
  sortedPanel(paths,`paths.${kind}`,data.paths,[["id","路徑編號",p=>p.id],["rule","規則",p=>p.rule],["chain","代理鏈",p=>(p.chains||[]).join(' → ')]],{field:'id',desc:false},p=>row(textOrUnknown(p.rule),`${p.rule_payload || '無規則載荷'} · ${Array.isArray(p.chains) && p.chains.length ? [...p.chains].reverse().join(' → ') : '鏈未知'}`,`#${p.id}`,kind === 'proxy_path' ? undefined : detailURL('proxy_path',String(p.id))),'目前沒有路徑投影。','最多顯示 100 條路徑；排序僅作用於已載入結果。');
  const samples = panel('最近樣本');
  sortedPanel(samples,`samples.${kind}`,data.recent_samples,[["last","最近觀測",s=>s.last_seen_at_ms],["first","首次觀測",s=>s.first_seen_at_ms],["target","目標",s=>s.target||s.destination_ip],["state","狀態",s=>s.state]],{field:'last',desc:true},s => {
    const fragment=document.createDocumentFragment();
    const sample = row(s.target || s.destination_ip || '未知目標',`${s.target_source} · ${textOrUnknown(s.destination_ip)} · ${textOrUnknown(s.destination_asn)}`,`${s.state} · ${when(s.last_seen_at_ms)}`,s.target ? detailURL(s.target_kind,s.target) : undefined);
    sample.classList.add('sample-row'); fragment.append(sample);
    if (s.destination_ip && kind !== 'destination_ip') fragment.append(row('查看目標 IP',s.destination_ip,'→',detailURL('destination_ip',s.destination_ip)));
    return fragment;
  },'原始連線細節已過期或尚未採集；保留的日統計仍可查閱。','最近 30 筆樣本；排序僅作用於已載入結果。');
  put(cols,paths,samples); content.append(cols);
  if (kind === 'proxy_path') {
    const carried = panel('此路徑承載的目標');
    sortedPanel(carried,'carried_targets',data.carried_targets,[["name","目標",t=>t.value],["kind","類型",t=>t.kind],["connections","連線數",t=>t.observed_connections],["bytes","流量",t=>t.observed_bytes]],{field:'connections',desc:true},t=>row(t.value,labels[t.kind] || t.kind,`日計數合計 ${number.format(t.observed_connections)} · ${bytes(t.observed_bytes)}`,detailURL(t.kind,t.value)),'目前沒有保留的目標聚合資料。','最多顯示 30 個目標；排序僅作用於已載入結果。');
    content.append(carried);
  }
  if (data.related_problems?.length) {
    const related = panel('關聯問題證據');
    sortedPanel(related,`related.${kind}`,data.related_problems,[["last","最近證據",p=>p.last_evidence_at_ms],["target","目標",p=>p.target],["kind","類型",p=>p.kind],["confidence","把握度",p=>p.confidence],["samples","樣本數",p=>p.sample_count]],{field:'last',desc:true},p=>row(problemLabels[p.kind] || p.kind,`${p.target} · 把握度 ${p.confidence}/3 · ${when(p.last_evidence_at_ms)}`,`${number.format(p.sample_count)} 樣本`,kind === 'proxy_path' ? detailURL(p.target_kind,p.target) : '#/problems'),'沒有關聯問題。','最多顯示 30 筆；排序僅作用於已載入結果。');
    content.append(related);
    const comparisons = data.related_problems.filter(p => p.kind === 'ipv6_observation_difference');
    if (comparisons.length) {
      const comparison = panel('IPv4 / IPv6 可比樣本');
      comparisons.forEach(p => {
        const e = p.evidence || {}, sub=node('section','comparison-subpanel');
        sub.append(put(node('div','panel-head'),node('h3','',`路徑 #${p.route_id} 的版本對照`)));
        sortableTable(sub,`ipv6.compare.${p.route_id}`,['ipv4','ipv6'],[["version","版本",v=>v],["eligible","符合比較的連線",v=>e[`${v}_eligible`]],["suspect","具觀測迹象的連線",v=>e[`${v}_suspect`]],["hours","覆蓋完整小時",v=>e[`${v}_observed_hours`]]],{field:'version',desc:false},v=>[v.toUpperCase(),e[`${v}_eligible`] ?? '未知',e[`${v}_suspect`] ?? '未知',e[`${v}_observed_hours`] ?? '未知']);
        sub.append(node('p','row-meta',`域名來源 ${textOrUnknown(e.domain_source)}。這些是重複或小流量長連線跡象，不是失敗率或回退證明。`));
        const hours = new Map();
        data.hourly.filter(h => h.route_id === p.route_id && (h.ip_version === 4 || h.ip_version === 6)).forEach(h => {
          const current = hours.get(h.bucket_start_ms) || {4:null,6:null};
          current[h.ip_version] = (current[h.ip_version] || 0) + h.observed_connections; hours.set(h.bucket_start_ms,current);
        });
        if (hours.size) {
          const hoursSection=node('section','comparison-subpanel');hoursSection.append(put(node('div','panel-head'),node('h3','','同一路徑的小時趨勢')));
          sortableTable(hoursSection,`ipv6.hours.${p.route_id}`,[...hours].sort((a,b)=>b[0]-a[0]).slice(0,48),[["time","小時",x=>x[0]],["v4","IPv4 已觀測",x=>x[1][4]],["v6","IPv6 已觀測",x=>x[1][6]]],{field:'time',desc:true},([bucket,counts])=>[when(bucket),counts[4]===null?'未觀測':number.format(counts[4]),counts[6]===null?'未觀測':number.format(counts[6])]);
          sub.append(hoursSection);
        }
        comparison.append(sub);
      });
      content.append(comparison);
    }
  }
  let historyPanel;
  const seenHistory = new Set(), historyEvents=[];
  let historyBody;
  function addHistory(events) {
    events.forEach(event => { if (!seenHistory.has(event.id)) { seenHistory.add(event.id); historyEvents.push(event); } });
    if (historyBody) historyBody.redraw();
  }
  if (kind === 'domain' || kind === 'ip_only' || kind === 'proxy_path') {
    historyPanel = panel('當時的問題摘要');
    historyBody=sortedPanel(historyPanel,`history.${kind}`,historyEvents,[["time","時間",e=>e.window_start_ms],["target","目標",e=>e.target],["kind","類型",e=>e.kind],["samples","樣本數",e=>e.sample_count]],{field:'time',desc:true},historyEntry,'目前載入的日期沒有保留的問題證據。');
    addHistory(data.problem_history || []); content.append(historyPanel);
  }
  if (data.grouping_changes?.length) {
    const grouping = panel('目標歸組變更');
    sortedPanel(grouping,`grouping.${kind}`,data.grouping_changes,[["time","變更時間",c=>c.changed_at_ms],["from","原目標",c=>c.from_value],["to","新目標",c=>c.to_value]],{field:'time',desc:true},change=>row(`${change.from_value || '未知'} → ${change.to_value || '未知'}`,`${change.from_kind} → ${change.to_kind} · 來源 ${change.to_source}`,when(change.changed_at_ms),detailURL(change.to_kind,change.to_value)),'沒有歸組變更。');
    content.append(grouping);
  }
  content.append(trendRows(data.hourly,'小時',data.gaps || []));
  let dailyRows = data.daily;
  let dailyPanel = trendRows(dailyRows,'每日',[]);
  function addOlderButton() {
    if (new Set(dailyRows.map(x => x.bucket_start_ms)).size % 90 !== 0) return;
    const button = node('button','filter','載入更早日歷史'); button.type = 'button';
    button.addEventListener('click',async () => {
      button.disabled = true; button.textContent = '讀取中…';
      try {
        const before = Math.min(...dailyRows.map(x => x.bucket_start_ms));
        const next = await get(`/api/history/${encodeURIComponent(kind)}/${encodeURIComponent(value)}?before_ms=${before}`);
        if (!next.length) { button.remove(); return; }
        if (historyPanel) {
          const start = Math.min(...next.map(x => x.bucket_start_ms));
          const end = Math.max(...next.map(x => x.bucket_start_ms)) + 2*86400000;
          const events = await get(`/api/problem-history/${encodeURIComponent(kind)}/${encodeURIComponent(value)}?start_ms=${start}&end_ms=${end}`);
          addHistory(events);
        }
        dailyRows = [...dailyRows,...next];
        const replacement = trendRows(dailyRows,'每日',[]);
        dailyPanel.replaceWith(replacement); dailyPanel = replacement;
        if (new Set(next.map(x => x.bucket_start_ms)).size === 90) addOlderButton();
      } catch (error) { button.disabled = false; button.textContent = '重試載入更早日歷史'; }
    });
    dailyPanel.append(button);
  }
  if (dailyRows.length) addOlderButton();
  content.append(dailyPanel);
  app.replaceChildren(content);
}

function flowCard(item,range) {
  const detail=node('details','flow-card'), summary=node('summary','flow-summary');
  const total=(item.upload_bytes||0)+(item.download_bytes||0);
  const lead=node('div','row-main');
  put(lead,node('strong','',item.target||'未知目標'),node('span','row-meta',`${item.rule||'規則未知'} · ${when(item.last_seen_at_ms)}`));
  const value=node('span','row-value',`${bytes(total)} · ${number.format(item.connections)} 筆`);
  put(summary,lead,value); detail.append(summary);
  const body=node('div','flow-body'),path=node('ol','hop-list');
  (item.hops||[...(item.chains||[])].reverse().map(name=>({name}))).forEach(hop=>{
    const locations=hop.locations||{}, entry=locations.entry, exit=locations.exit;
    const li=node('li','',hop.name);
    const notes=[];
    if (entry) notes.push(`入口：${entry.label}（${entry.accuracy}，${entry.source}）`);
    if (exit) notes.push(`出口：${exit.label}（${exit.accuracy}，${exit.source}）`);
    if (!entry && !exit) notes.push('地理位置未知');
    li.append(node('div','row-meta',notes.join(' · '))); path.append(li);
  });
  if (path.childNodes.length) body.append(path);
  if (item.dialer_proxy) {
    const candidates=node('details','candidate-list'), summary=node('summary','',`前置：${item.dialer_proxy} · 具體第一跳未知`);
    candidates.append(summary);
    const list=item.first_hop_candidates||[];
    candidates.append(node('p','row-meta',`當時已知的拓撲版本始於 ${when(item.topology_at_ms)}。以下 ${number.format(list.length)} 個節點是候選，不能證明這條連線實際使用了哪一個。`));
    const ul=node('ul','candidate-items');
    const {element,state}=sortControls(`candidates.${item.dialer_proxy}`,[['name','節點名稱',x=>x]],{field:'name',desc:false},drawCandidates);
    candidates.append(element,ul); body.append(candidates);
    function drawCandidates() {ul.replaceChildren();[...list].sort((a,b)=>(state.desc?-1:1)*compareValue(a,b)).forEach(name=>ul.append(node('li','',name)));}
    drawCandidates();
  }
  body.append(node('p','row-meta',`目標 IP：${item.destination_ip||'未知'} · 位置：${item.target_point?`${item.target_point.label}（${item.target_point.accuracy}，${item.target_point.source}）`:'未知'} · 路徑 #${item.route_id}`));
  const actions=node('div','flow-actions'); actions.append(link('目標詳情 →',detailURL(item.target_kind,item.target))); actions.append(link('路徑詳情 →',detailURL('proxy_path',String(item.route_id))));
  const samples=node('div','flow-samples'), samplePager=node('div','pager');
  const button=node('button','filter','查看連線樣本'); button.type='button';
  let samplePage=1,loaded=false;
  const sampleSort=sortControls('flow.samples',[['time','最後觀測',s=>s.last_seen_at_ms],['bytes','流量',s=>s.upload_bytes+s.download_bytes],['state','狀態',s=>s.state],['ip','目標 IP',s=>s.destination_ip]],{field:'time',desc:true},()=>{if(loaded)loadSamples(1);});
  async function loadSamples(page) {
    button.disabled=true;button.textContent='載入中…';
    try {
      const params=new URLSearchParams({target:item.target,route_id:String(item.route_id),start_ms:String(range.start),end_ms:String(range.end),page:String(page),sort:sampleSort.state.field,dir:sampleSort.state.desc?'desc':'asc'});
      const data=await get(`/api/flow-samples?${params}`);
      loaded=true;samplePage=page;samples.replaceChildren();samplePager.replaceChildren();
      if (!data.items.length) samples.append(node('p','row-meta','此時間範圍沒有保留的原始連線樣本；路徑聚合仍可查看。'));
      data.items.forEach(s=>samples.append(row(when(s.last_seen_at_ms),`${s.state} · ${s.destination_ip||'目標 IP 未知'}`,bytes(s.upload_bytes+s.download_bytes))));
      const totalPages=Math.max(1,Math.ceil(data.total/data.limit));
      const prev=node('button','filter','上一頁'),next=node('button','filter','下一頁');
      prev.type=next.type='button';prev.disabled=page<=1;next.disabled=page>=totalPages;
      prev.addEventListener('click',()=>loadSamples(samplePage-1));next.addEventListener('click',()=>loadSamples(samplePage+1));
      put(samplePager,prev,node('span','row-meta',`第 ${page} / ${totalPages} 頁 · 共 ${number.format(data.total)} 筆`),next);
      button.remove();
    } catch(error) {button.disabled=false;button.textContent='讀取失敗，重試樣本';samples.prepend(node('p','row-meta',error.message));}
  }
  button.addEventListener('click',()=>loadSamples(samplePage));
  put(actions,button); put(body,actions,sampleSort.element,samples,samplePager); detail.append(body); return detail;
}

async function flowPage(token,initialTarget) {
  setNav('flows'); statusNode.textContent='流向地圖';
  const content=document.createDocumentFragment();
  content.append(title('FLOWS / 04','流向地圖','線條表示已觀測的邏輯代理順序，地理位置是離線估計；不代表封包的物理路線。','即時與歷史'));
  const toolbar=node('div','flow-toolbar');
  const mode=node('select','filter'); mode.setAttribute('aria-label','資料模式');
  [['history','歷史聚合'],['live','目前連線']].forEach(([v,l])=>{const o=node('option','',l);o.value=v;mode.append(o);});
  const period=node('select','filter'); period.setAttribute('aria-label','歷史時間範圍');
  [['24','最近 24 小時'],['168','最近 7 天'],['720','最近 30 天'],['custom','自訂時間']].forEach(([v,l])=>{const o=node('option','',l);o.value=v;period.append(o);});
  const startInput=node('input','filter'),endInput=node('input','filter'); startInput.type=endInput.type='datetime-local'; startInput.setAttribute('aria-label','起始時間');endInput.setAttribute('aria-label','結束時間');
  const apply=node('button','filter','套用時間'); apply.type='button';
  const targetInput=node('input','filter'); targetInput.type='search'; targetInput.placeholder='篩選目標'; targetInput.value=initialTarget; targetInput.setAttribute('aria-label','篩選目標');
  const nodeInput=node('input','filter'); nodeInput.type='search'; nodeInput.placeholder='篩選代理節點'; nodeInput.setAttribute('aria-label','篩選代理節點');
  put(toolbar,mode,period,startInput,endInput,apply,targetInput,nodeInput); content.append(toolbar);
  const mapPanel=panel('全球路徑'); mapPanel.classList.add('map-panel');
  const svg=document.createElementNS('http://www.w3.org/2000/svg','svg'); svg.classList.add('world-map');
  const mapInfo=node('p','map-info','讀取本地地圖資料…'); put(mapPanel,svg,mapInfo); content.append(mapPanel);
  const routePanel=panel('連線與路徑'); content.append(routePanel);
  const routeItems=[]; let currentRange={start:Date.now()-86400000,end:Date.now()};
  const routeBody=sortedPanel(routePanel,'flows',routeItems,[["bytes","流量",f=>f.upload_bytes+f.download_bytes],["last","最後觀測",f=>f.last_seen_at_ms],["target","目標",f=>f.target],["connections","連線數",f=>f.connections],["route","路徑編號",f=>f.route_id]],{field:'bytes',desc:true},f=>flowCard(f,currentRange),'此範圍沒有已觀測的連線。',null,50);
  app.replaceChildren(content);
  const geo=await WorldMap.load(); if (token!==renderToken) return;
  let latest=[],origin=null,fetchSerial=0,geoVersion='',geoProblem='',dailyRounded=false;
  function range() {
    const now=Date.now();
    if (mode.value==='live') return {start:now-86400000,end:now};
    if (period.value!=='custom') return {start:now-Number(period.value)*3600000,end:now};
    const start=new Date(startInput.value).getTime(),end=new Date(endInput.value).getTime();
    if (!Number.isFinite(start)||!Number.isFinite(end)||end<=start) throw new Error('請輸入有效的起始和結束時間。');
    return {start,end};
  }
  function showCustom() {const custom=period.value==='custom' && mode.value==='history';startInput.hidden=endInput.hidden=apply.hidden=!custom;period.hidden=mode.value==='live';}
  function filterDraw() {
    const target=targetInput.value.trim().toLocaleLowerCase(),proxy=nodeInput.value.trim().toLocaleLowerCase();
    const filtered=latest.filter(f=>(!target||f.target.toLocaleLowerCase().includes(target)) && (!proxy||(f.chains||[]).some(n=>n.toLocaleLowerCase().includes(proxy))||(f.first_hop_candidates||[]).some(n=>n.toLocaleLowerCase().includes(proxy))));
    routeItems.splice(0,routeItems.length,...filtered);routeBody.redraw();
    const count=WorldMap.draw(svg,geo,filtered,origin,selected=>{targetInput.value=selected;filterDraw();});
    mapInfo.textContent=`${number.format(filtered.length)} 條目 · ${number.format(count.edges)} 段可定位關聯 · ${number.format(count.points)} 個地理點${geoVersion?` · MMDB ${geoVersion}`:geoProblem?` · ${geoProblem}`:' · 未配置 MMDB'}。未知位置與第一跳見下方列表。歷史位置以目前資料重新估計。${dailyRounded?'此範圍使用每日統計，起始日期按完整報表日計入。':''}`;
  }
  async function refresh() {
    if (token!==renderToken) return;
    const id=++fetchSerial;
    try {
      currentRange=range();
      const params=new URLSearchParams({mode:mode.value,start_ms:String(currentRange.start),end_ms:String(currentRange.end)});
      const data=await get(`/api/flows?${params}`); if(id!==fetchSerial||token!==renderToken)return;
      latest=data.items||[];origin=data.origin;geoVersion=data.geoip_version||'';geoProblem=data.geoip_error||'';dailyRounded=!!data.range_rounded_to_day;
      statusNode.textContent=data.interruption_reason ? `採集已過期 · ${when(data.as_of_ms)}` : data.as_of_ms ? `資料時間 ${when(data.as_of_ms)}` : '尚未採集';
      filterDraw();
    } catch(error) { if(id===fetchSerial) {statusNode.textContent='流向資料讀取失敗';mapInfo.textContent=error.message;} }
  }
  showCustom();
  mode.addEventListener('change',()=>{showCustom();refresh();});period.addEventListener('change',()=>{showCustom();if(period.value!=='custom')refresh();});apply.addEventListener('click',refresh);
  targetInput.addEventListener('input',filterDraw);nodeInput.addEventListener('input',filterDraw);
  await refresh();
  flowTimer=setInterval(()=>{if(mode.value==='live') refresh();},2000);
}

async function render() {
  const token = ++renderToken;
  if (flowTimer) { clearInterval(flowTimer); flowTimer=undefined; }
  app.replaceChildren(node('div','loading','正在讀取觀測資料…'));
  const parts = (location.hash || '#/').slice(2).split('/');
  try {
    if (parts[0] === 'targets') await targets(token);
    else if (parts[0] === 'flows') await flowPage(token,parts.length>1?decodeURIComponent(parts.slice(1).join('/')):'');
    else if (parts[0] === 'problems') await problems(token);
    else if (parts[0] === 'detail' && parts.length >= 3) await detail(token,decodeURIComponent(parts[1]),decodeURIComponent(parts.slice(2).join('/')));
    else await dashboard(token);
  } catch (error) { if (token === renderToken) showError(error); }
}
window.addEventListener('hashchange', render);
setInterval(() => { if (!location.hash || location.hash === '#/') render(); },5000);
render();
