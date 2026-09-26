/* Observer renders API values as text, never as HTML. */
const app = document.getElementById('app');
const statusNode = document.getElementById('live-status');
const dateTime = new Intl.DateTimeFormat('zh-TW', {dateStyle: 'short', timeStyle: 'medium'});
const number = new Intl.NumberFormat('zh-TW');
const labels = {domain:'域名', ip_only:'僅 IP', destination_ip:'目標 IP', asn:'ASN', proxy_path:'代理路徑'};
const problemLabels = {repeated_connections:'重複連線跡象', small_long_connections:'小流量長連線補充證據', ipv6_observation_difference:'值得檢查 IPv6 路徑'};
let renderToken = 0;

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
async function get(path) {
  const response = await fetch(path,{cache:'no-store'});
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
  if (!data.recent_gaps?.length) gaps.append(empty('最近 7 日沒有已記錄的缺口。'));
  else data.recent_gaps.forEach(g => gaps.append(row(when(g.started_at_ms),`${g.stream} · ${g.reason}${g.dropped_samples ? ` · 丟幀 ${g.dropped_samples}` : ''}`,g.ended_at_ms ? `至 ${when(g.ended_at_ms)}` : '仍在持續')));
  const guide = panel('下一步');
  put(guide,row('查看目標','按完整域名或目標 IP 查看已觀測路徑','→','#/targets'),row('閱讀問題證據','計數與檢查方向由實際快照推導','→','#/problems'));
  if (status.collector?.read_errors) guide.append(row('Controller 讀取錯誤','連線或憑據可能需要檢查',number.format(status.collector.read_errors)));
  put(columns,gaps,guide); content.append(columns); app.replaceChildren(content);
}

async function targets(token) {
  setNav('targets'); statusNode.textContent = '歷史目標';
  const data = await get('/api/targets'); if (token !== renderToken) return;
  const content = document.createDocumentFragment();
  content.append(title('TARGETS / 02','已觀測目標','列出保留的日統計中存在的域名與 IP；不進行反向 DNS 猜測。','日統計歷史'));
  const section = panel('目標列表');
  if (!data.length) section.append(empty('目前沒有可顯示的目標；等待快照和首次投影。'));
  data.forEach(item => section.append(row(item.value,`${labels[item.kind] || item.kind} · 最後觀測 ${when(item.last_seen_at_ms)}`,`日計數合計 ${number.format(item.observed_connections)} · ${bytes(item.observed_upload_bytes + item.observed_download_bytes)}`,detailURL(item.kind,item.value))));
  content.append(section); app.replaceChildren(content);
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
  const data = await get('/api/problems'); if (token !== renderToken) return;
  const content = document.createDocumentFragment();
  content.append(title('EVIDENCE / 03','問題證據','這些模式提示檢查方向；它們不是請求失敗或分流錯誤的判定。','最近 7 日'));
  const section = panel('達到門檻的觀測模式');
  if (!data.length) section.append(empty('目前沒有達到展示門檻的問題證據。'));
  data.forEach(p => section.append(evidenceDetails(p)));
  content.append(section); app.replaceChildren(content);
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
    gaps.slice(0,5).forEach(g => section.append(node('div','row-meta',`${when(g.started_at_ms)} 至 ${when(g.ended_at_ms)} · ${g.reason}`)));
  }
  const table = node('div','table-wrap'), t = node('table'), thead = node('thead'), tr = node('tr');
  ['時間','連線','上傳','下載'].forEach(x => tr.append(node('th','',x))); thead.append(tr); t.append(thead);
  const tbody = node('tbody'); ordered.reverse().slice(0,granularity === '每日' ? ordered.length : 80).forEach(([bucket,x]) => { const r = node('tr'); [when(bucket),x ? number.format(x.count) : '未知（缺口）',x ? bytes(x.up) : '未知',x ? bytes(x.down) : '未知'].forEach(v => r.append(node('td','',v))); tbody.append(r); });
  t.append(tbody); table.append(t); section.append(table); return section;
}
async function detail(token, kind, value) {
  setNav(''); statusNode.textContent = labels[kind] || kind;
  const data = await get(`/api/details/${encodeURIComponent(kind)}/${encodeURIComponent(value)}`);
  if (token !== renderToken) return;
  const content = document.createDocumentFragment();
  const heading = kind === 'proxy_path' ? textOrUnknown(data.paths?.[0]?.rule) : value;
  const intro = kind === 'proxy_path' ? `路徑 #${value} · ${data.paths?.[0]?.rule_payload || '無規則載荷'} · ${Array.isArray(data.paths?.[0]?.chains) && data.paths[0].chains.length ? data.paths[0].chains.join(' → ') : '代理鏈未知'}` : '歷史資料按實際觀測路徑歸組；缺失欄位保持未知。';
  content.append(title(`${labels[kind] || kind} / DETAIL`,heading,intro,`最近 30 日 · ${data.hourly.length} 條小時統計`));
  if (kind === 'domain') {
    const sources = panel('域名來源 · 原始資料保留期');
    if (!data.observed_sources?.length) sources.append(empty('來源未知；原始記錄可能已過期。'));
    data.observed_sources?.forEach(source => sources.append(row(source.source,`首次 ${when(source.first_seen_at_ms)} · 最近 ${when(source.last_seen_at_ms)}`,`${number.format(source.observed_connections)} 條`)));
    content.append(sources);
  }
  const cols = node('div','two-column');
  const paths = panel('實際觀測路徑');
  if (!data.paths.length) paths.append(empty('目前沒有路徑投影。'));
  data.paths.forEach(p => paths.append(row(textOrUnknown(p.rule),`${p.rule_payload || '無規則載荷'} · ${Array.isArray(p.chains) && p.chains.length ? p.chains.join(' → ') : '鏈未知'}`,`#${p.id}`,kind === 'proxy_path' ? undefined : detailURL('proxy_path',String(p.id)))));
  const samples = panel('最近樣本');
  if (!data.recent_samples.length) samples.append(empty('原始連線細節已過期或尚未採集；保留的日統計仍可查閱。'));
  data.recent_samples.slice(0,12).forEach(s => {
    const sample = row(s.target || s.destination_ip || '未知目標',`${s.target_source} · ${textOrUnknown(s.destination_ip)} · ${textOrUnknown(s.destination_asn)}`,`${s.state} · ${when(s.last_seen_at_ms)}`,s.target ? detailURL(s.target_kind,s.target) : undefined);
    sample.classList.add('sample-row'); samples.append(sample);
    if (s.destination_ip && kind !== 'destination_ip') samples.append(row('查看目標 IP',s.destination_ip,'→',detailURL('destination_ip',s.destination_ip)));
  });
  put(cols,paths,samples); content.append(cols);
  if (kind === 'proxy_path') {
    const carried = panel('此路徑承載的目標');
    if (!data.carried_targets?.length) carried.append(empty('目前沒有保留的目標聚合資料。'));
    data.carried_targets?.forEach(t => carried.append(row(t.value,labels[t.kind] || t.kind,`日計數合計 ${number.format(t.observed_connections)} · ${bytes(t.observed_bytes)}`,detailURL(t.kind,t.value))));
    content.append(carried);
  }
  if (data.related_problems?.length) {
    const related = panel('關聯問題證據');
    data.related_problems.forEach(p => related.append(row(problemLabels[p.kind] || p.kind,`${p.target} · 把握度 ${p.confidence}/3 · ${when(p.last_evidence_at_ms)}`,`${number.format(p.sample_count)} 樣本`,kind === 'proxy_path' ? detailURL(p.target_kind,p.target) : '#/problems')));
    content.append(related);
    const comparisons = data.related_problems.filter(p => p.kind === 'ipv6_observation_difference');
    if (comparisons.length) {
      const comparison = panel('IPv4 / IPv6 可比樣本');
      comparisons.forEach(p => {
        const e = p.evidence || {}, wrap = node('div','table-wrap'), table = node('table');
        const head = node('tr'); ['版本','符合比較的連線','具觀測迹象的連線','覆蓋完整小時'].forEach(label => head.append(node('th','',label)));
        table.append(put(node('thead'),head)); const body = node('tbody');
        for (const version of ['ipv4','ipv6']) { const tr = node('tr'); [version.toUpperCase(),e[`${version}_eligible`] ?? '未知',e[`${version}_suspect`] ?? '未知',e[`${version}_observed_hours`] ?? '未知'].forEach(value => tr.append(node('td','',value))); body.append(tr); }
        table.append(body); wrap.append(table); comparison.append(wrap);
        comparison.append(node('p','row-meta',`域名來源 ${textOrUnknown(e.domain_source)} · 路徑 #${p.route_id}。這些是重複或小流量長連線跡象，不是失敗率或回退證明。`));
        const hours = new Map();
        data.hourly.filter(h => h.route_id === p.route_id && (h.ip_version === 4 || h.ip_version === 6)).forEach(h => {
          const current = hours.get(h.bucket_start_ms) || {4:null,6:null};
          current[h.ip_version] = (current[h.ip_version] || 0) + h.observed_connections; hours.set(h.bucket_start_ms,current);
        });
        if (hours.size) {
          comparison.append(node('h3','', '同一路徑的小時趨勢'));
          const trendTable = node('table'), trendHead = node('tr');
          ['小時','IPv4 已觀測','IPv6 已觀測'].forEach(label => trendHead.append(node('th','',label)));
          trendTable.append(put(node('thead'),trendHead)); const trendBody = node('tbody');
          [...hours].sort((a,b) => b[0]-a[0]).slice(0,48).forEach(([bucket,counts]) => {
            const tr = node('tr'); [when(bucket),counts[4] === null ? '未觀測' : number.format(counts[4]),counts[6] === null ? '未觀測' : number.format(counts[6])].forEach(v => tr.append(node('td','',v))); trendBody.append(tr);
          });
          trendTable.append(trendBody); comparison.append(put(node('div','table-wrap'),trendTable));
        }
      });
      content.append(comparison);
    }
  }
  let historyPanel;
  let historyEmpty;
  const seenHistory = new Set();
  function addHistory(events) {
    events.forEach(event => { if (!seenHistory.has(event.id)) { seenHistory.add(event.id); historyPanel.append(historyEntry(event)); } });
    if (seenHistory.size && historyEmpty) { historyEmpty.remove(); historyEmpty = null; }
  }
  if (kind === 'domain' || kind === 'ip_only' || kind === 'proxy_path') {
    historyPanel = panel('當時的問題摘要');
    historyEmpty = empty('目前載入的日期沒有保留的問題證據。'); historyPanel.append(historyEmpty);
    addHistory(data.problem_history || []); content.append(historyPanel);
  }
  if (data.grouping_changes?.length) {
    const grouping = panel('目標歸組變更');
    data.grouping_changes.forEach(change => grouping.append(row(`${change.from_value || '未知'} → ${change.to_value || '未知'}`,`${change.from_kind} → ${change.to_kind} · 來源 ${change.to_source}`,when(change.changed_at_ms),detailURL(change.to_kind,change.to_value))));
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

async function render() {
  const token = ++renderToken;
  app.replaceChildren(node('div','loading','正在讀取觀測資料…'));
  const parts = (location.hash || '#/').slice(2).split('/');
  try {
    if (parts[0] === 'targets') await targets(token);
    else if (parts[0] === 'problems') await problems(token);
    else if (parts[0] === 'detail' && parts.length >= 3) await detail(token,decodeURIComponent(parts[1]),decodeURIComponent(parts.slice(2).join('/')));
    else await dashboard(token);
  } catch (error) { if (token === renderToken) showError(error); }
}
window.addEventListener('hashchange', render);
setInterval(() => { if (!location.hash || location.hash === '#/') render(); },5000);
render();
