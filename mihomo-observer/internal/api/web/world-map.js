/* Natural Earth 1:110m geography is served from this application's own assets. */
window.WorldMap = (() => {
  const ns='http://www.w3.org/2000/svg';
  let geography;
  async function load() {
    if (!geography) geography=fetch(new URL('/assets/world.geojson',location.origin),{cache:'force-cache'}).then(r=>{if(!r.ok) throw new Error('地圖底圖讀取失敗');return r.json();}).catch(error=>{geography=undefined;throw error;});
    return geography;
  }
  function xy(point) { return [(point.lon+180)*1000/360,(90-point.lat)*500/180]; }
  function element(tag,attrs={}) { const e=document.createElementNS(ns,tag); Object.entries(attrs).forEach(([k,v])=>e.setAttribute(k,String(v))); return e; }
  function ringPath(ring) {
    let path='',previous;
    for (const coords of ring) {
      const p=xy({lon:coords[0],lat:coords[1]});
      path+=(previous && Math.abs(p[0]-previous[0])<500 ? 'L' : 'M')+`${p[0].toFixed(1)},${p[1].toFixed(1)}`;
      previous=p;
    }
    return path+'Z';
  }
  function landPath(feature) {
    const geometry=feature.geometry;
    if (geometry.type==='Polygon') return geometry.coordinates.map(ringPath).join('');
    if (geometry.type==='MultiPolygon') return geometry.coordinates.flatMap(p=>p.map(ringPath)).join('');
    return '';
  }
  function isGroup(type) { return ['Selector','URLTest','Fallback','LoadBalance','Relay'].includes(type); }
  function stages(flow,origin) {
    const points=[];
    if (origin) points.push({point:origin,label:'觀測起點'});
    if (flow.dialer_proxy) points.push({point:null,label:'具體第一跳未知'});
    const hops=flow.hops||[];
    if (!hops.length) points.push({point:null,label:'代理路徑未知'});
    for (const hop of hops) {
      if (isGroup(hop.type)) continue;
      if (hop.name==='DIRECT') continue;
      const entry=hop.locations?.entry, exit=hop.locations?.exit;
      if (entry) points.push({point:entry,label:`${hop.name} · 入口`});
      if (exit) points.push({point:exit,label:`${hop.name} · 出口`});
      if (!entry && !exit) points.push({point:null,label:`${hop.name} · 位置未知`});
    }
    points.push({point:flow.target_point||null,label:flow.target});
    return points;
  }
  function buildEdges(flows,origin) {
    const edgeMap=new Map(),points=[];
    for (const flow of flows) {
      const stagesForFlow=stages(flow,origin),weight=(flow.upload_bytes||0)+(flow.download_bytes||0);
      stagesForFlow.forEach(stage=>{if(stage.point) points.push(stage);});
      for (let i=1;i<stagesForFlow.length;i++) {
        const from=stagesForFlow[i-1],to=stagesForFlow[i];
        if (!from.point || !to.point) continue;
        const key=[from.point.lat.toFixed(1),from.point.lon.toFixed(1),to.point.lat.toFixed(1),to.point.lon.toFixed(1)].join('|');
        const old=edgeMap.get(key)||{from,to,bytes:0,connections:0,targets:new Set()};
        old.bytes+=weight; old.connections+=flow.connections||0; old.targets.add(flow.target); edgeMap.set(key,old);
      }
    }
    return {edges:[...edgeMap.values()],points};
  }
  function arc(a,b) {
    const [x1,y1]=xy(a),[x2,y2]=xy(b),dx=x2-x1,dy=y2-y1;
    const lift=Math.max(8,Math.min(66,Math.hypot(dx,dy)*.16));
    if (Math.abs(dx)>500) {
      const boundary=dx>0?0:1000,other=dx>0?1000:0,wrappedX2=dx>0?x2-1000:x2+1000;
      const fraction=(boundary-x1)/(wrappedX2-x1),crossY=y1+(y2-y1)*fraction-lift;
      return `M${x1.toFixed(1)},${y1.toFixed(1)} Q${((x1+boundary)/2).toFixed(1)},${((y1+crossY)/2-lift/2).toFixed(1)} ${boundary},${crossY.toFixed(1)} M${other},${crossY.toFixed(1)} Q${((other+x2)/2).toFixed(1)},${((crossY+y2)/2-lift/2).toFixed(1)} ${x2.toFixed(1)},${y2.toFixed(1)}`;
    }
    return `M${x1.toFixed(1)},${y1.toFixed(1)} Q${((x1+x2)/2).toFixed(1)},${((y1+y2)/2-lift).toFixed(1)} ${x2.toFixed(1)},${y2.toFixed(1)}`;
  }
  function draw(svg,geo,flows,origin,onSelect) {
    svg.replaceChildren(); svg.setAttribute('viewBox','0 0 1000 500'); svg.setAttribute('role','img');
    svg.setAttribute('aria-label','世界地圖：已觀測的邏輯代理路徑；未知位置未連線。下方列表有完整文字資訊。');
    const graticule=element('g',{class:'map-graticule'});
    for(let lon=-150;lon<=150;lon+=30){const x=xy({lat:0,lon})[0];graticule.append(element('path',{d:`M${x},0 V500`}));}
    for(let lat=-60;lat<=60;lat+=30){const y=xy({lat,lon:0})[1];graticule.append(element('path',{d:`M0,${y} H1000`}));}
    svg.append(graticule);
    const land=element('g',{class:'map-land'});
    geo.features.forEach(feature=>land.append(element('path',{d:landPath(feature)}))); svg.append(land);
    const built=buildEdges(flows,origin),max=built.edges.reduce((n,e)=>Math.max(n,e.bytes),1),lines=element('g',{class:'map-lines'});
    built.edges.forEach(e=>{
      const path=element('path',{d:arc(e.from.point,e.to.point),'stroke-width':(1.2+6*Math.sqrt(e.bytes/max)).toFixed(1),class:'map-edge'});
      const title=element('title'); title.textContent=`${e.from.label} → ${e.to.label} · ${e.connections} 筆分桶連線 · ${e.targets.size} 個目標`; path.append(title);
      if (e.targets.size===1 && onSelect) {path.setAttribute('tabindex','0');path.addEventListener('click',()=>onSelect([...e.targets][0]));path.addEventListener('keydown',event=>{if(event.key==='Enter') onSelect([...e.targets][0]);});}
      lines.append(path);
    }); svg.append(lines);
    const markers=element('g',{class:'map-markers'}),seen=new Set();
    built.points.forEach(({point,label})=>{
      const key=`${point.lat.toFixed(2)}|${point.lon.toFixed(2)}|${label}`; if(seen.has(key))return;seen.add(key);
      const [x,y]=xy(point),dot=element('circle',{cx:x.toFixed(1),cy:y.toFixed(1),r:3.5,class:'map-point'});
      const title=element('title'); title.textContent=`${label} · ${point.label} · ${point.accuracy} · ${point.source}`; dot.append(title); markers.append(dot);
    }); svg.append(markers);
    return {edges:built.edges.length,points:seen.size};
  }
  return {load,draw};
})();
