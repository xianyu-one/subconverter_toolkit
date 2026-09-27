import { hostFromUrl, pageIdentity, ruleOptions, uniqueRuleLines } from "./rules.js";

const $ = (id) => document.getElementById(id);
const ui = {
  tabName: $("tabName"), tabUrl: $("tabUrl"), recordState: $("recordState"),
  recordButton: $("recordButton"), refreshHint: $("refreshHint"), notice: $("notice"),
  metricRequests: $("metricRequests"), metricTargets: $("metricTargets"), metricSize: $("metricSize"),
  currentView: $("currentView"), historyView: $("historyView"), historyControls: $("historyControls"),
  historySelect: $("historySelect"), sessionHeading: $("sessionHeading"), sessionCaption: $("sessionCaption"),
  clearButton: $("clearButton"), searchInput: $("searchInput"), sortSelect: $("sortSelect"),
  targetList: $("targetList"), emptyState: $("emptyState"), emptyTitle: $("emptyTitle"),
  emptyDescription: $("emptyDescription"), selectionCount: $("selectionCount"), copySelected: $("copySelected")
};

let state = { recording: null, pages: [], removedPages: 0 };
let activeTab = null;
let viewMode = "current";
let historyId = null;
let expandedTargets = new Set();
const chosenRules = new Map();
const selectedRules = new Map();
let lastRemovedCount = null;
let refreshTimer = null;
let noticeTimer = null;

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function formatBytes(bytes) {
  if (bytes === null || bytes === undefined) return "未知";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let value = bytes;
  let unit = "B";
  for (const next of units) {
    value /= 1024;
    unit = next;
    if (value < 1024) break;
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${unit}`;
}

function formatDuration(ms) {
  if (ms === null || ms === undefined) return "未知";
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(2)} s`;
}

function showNotice(message, isError = false) {
  clearTimeout(noticeTimer);
  ui.notice.textContent = message;
  ui.notice.classList.toggle("is-error", isError);
  ui.notice.hidden = false;
  noticeTimer = setTimeout(() => { ui.notice.hidden = true; }, 6500);
}

async function send(message) {
  const response = await chrome.runtime.sendMessage(message);
  if (response?.error) throw new Error(response.error);
  return response;
}

async function readActiveTab() {
  const tabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  return tabs[0] || null;
}

async function refresh() {
  const [response, tab] = await Promise.all([send({ type: "GET_STATE" }), readActiveTab()]);
  state = response.state;
  activeTab = tab;
  if (lastRemovedCount !== null && state.removedPages > lastRemovedCount) {
    showNotice("已移除最早的网页记录，以保留最近 50 个网页。");
  }
  lastRemovedCount = state.removedPages;
  render();
}

function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => { void refresh().catch((error) => showNotice(error.message, true)); }, 120);
}

function currentSession() {
  if (!activeTab) return null;
  const identity = pageIdentity(activeTab.url);
  return state.pages.find((page) => page.tabId === activeTab.id && page.pageUrl === identity) || null;
}

function viewedSession() {
  if (viewMode === "current") return currentSession();
  return state.pages.find((page) => page.id === historyId) || state.pages[0] || null;
}

function aggregate(requests) {
  const groups = new Map();
  for (const request of requests) {
    const host = hostFromUrl(request.url);
    if (!host) continue;
    const group = groups.get(host) || { host, requests: [], ips: new Set(), slowest: 0, durationTotal: 0, knownSize: 0, knownCount: 0 };
    group.requests.push(request);
    group.slowest = Math.max(group.slowest, request.durationMs || 0);
    group.durationTotal += request.durationMs || 0;
    if (request.sizeBytes !== null) {
      group.knownSize += request.sizeBytes;
      group.knownCount += 1;
    }
    if (request.ip) group.ips.add(request.ip);
    groups.set(host, group);
  }
  return [...groups.values()];
}

function groupOptions(group) {
  const domain = (host) => chrome.publicSuffix.getDomain(host);
  const suffix = (host) => chrome.publicSuffix.isKnownSuffix(host);
  return ruleOptions(group.host, [...group.ips], domain, suffix);
}

function selectionKey(session, host) {
  return `${session.id}\n${host}`;
}

function renderRequest(request, recordsById) {
  const details = node("details", "request");
  const summary = node("summary");
  const url = new URL(request.url);
  const path = `${url.pathname || "/"}`;
  summary.append(node("span", "request-path", path), node("span", "request-duration", formatDuration(request.durationMs)));
  const sub = node("span", "request-sub");
  const status = request.status === "error" ? "失败" : request.status === "redirect" ? `跳转 ${request.statusCode || ""}` : `${request.statusCode || "完成"}`;
  sub.append(
    node("span", `request-status${request.status === "error" ? " is-error" : request.status === "redirect" ? " is-redirect" : ""}`, status),
    node("span", "", request.type === "websocket" ? "WebSocket 握手" : request.type),
    node("span", "", formatBytes(request.sizeBytes))
  );
  summary.append(sub);
  details.append(summary);
  const body = node("div", "request-detail");
  const urlLine = node("div", "", "完整 URL · ");
  urlLine.append(node("code", "", request.url));
  body.append(urlLine);
  if (request.ip) body.append(node("div", "", `实际连接 IP · ${request.ip}`));
  if (request.redirectTo) body.append(node("div", "", `跳转至 · ${request.redirectTo}`));
  if (request.redirectFrom) body.append(node("div", "", `上一跳 · ${recordsById.get(request.redirectFrom)?.url || request.redirectFrom}`));
  if (request.error) body.append(node("div", "", `错误 · ${request.error}`));
  if (request.fromCache) body.append(node("div", "", "来自磁盘缓存"));
  details.append(body);
  return details;
}

function renderGroup(group, session) {
  const key = selectionKey(session, group.host);
  const options = groupOptions(group);
  const chosen = options.find((option) => option.text === chosenRules.get(key)) || options[0];
  const article = node("article", `target-group${expandedTargets.has(key) ? " is-open" : ""}`);
  const head = node("div", "target-head");
  const checkbox = node("input", "target-check");
  checkbox.type = "checkbox";
  checkbox.checked = selectedRules.has(key);
  checkbox.setAttribute("aria-label", `选择 ${group.host} 的规则`);
  checkbox.addEventListener("change", () => {
    if (checkbox.checked) selectedRules.set(key, chosenRules.get(key) || options[0]?.text);
    else selectedRules.delete(key);
    renderSelection(session);
  });
  const toggle = node("button", "target-toggle");
  toggle.type = "button";
  toggle.setAttribute("aria-expanded", String(expandedTargets.has(key)));
  toggle.append(node("span", "target-name", group.host), node("span", "target-chevron", "›"));
  toggle.append(node("span", "target-meta", `${group.requests.length} 次 · 最慢 ${formatDuration(group.slowest)} · 累计 ${formatDuration(group.durationTotal)} · ${group.knownCount ? formatBytes(group.knownSize) : "大小未知"}`));
  toggle.addEventListener("click", () => {
    if (expandedTargets.has(key)) expandedTargets.delete(key);
    else expandedTargets.add(key);
    renderTargets(session);
  });
  head.append(checkbox, toggle);
  article.append(head);
  if (expandedTargets.has(key)) {
    const body = node("div", "target-body");
    const picker = node("div", "rule-picker");
    const select = node("select", "rule-select");
    select.setAttribute("aria-label", `${group.host} 的候选规则`);
    for (const option of options) {
      const item = node("option", "", option.label);
      item.value = option.text;
      select.append(item);
    }
    select.value = chosen?.text || "";
    select.addEventListener("change", () => {
      chosenRules.set(key, select.value);
      if (selectedRules.has(key)) selectedRules.set(key, select.value);
      renderSelection(session);
    });
    const copy = node("button", "copy-one", "复制");
    copy.type = "button";
    copy.addEventListener("click", () => { void copyText(select.value); });
    picker.append(select, copy);
    body.append(picker);
    const list = node("div", "request-list");
    const recordsById = new Map(session.requests.map((request) => [request.id, request]));
    for (const request of [...group.requests].reverse()) list.append(renderRequest(request, recordsById));
    body.append(list);
    article.append(body);
  }
  return article;
}

function renderSelection(session) {
  const lines = session ? [...selectedRules.entries()].filter(([key]) => key.startsWith(`${session.id}\n`)).map(([, value]) => value) : [];
  ui.selectionCount.textContent = `${lines.length} 条`;
  ui.copySelected.disabled = lines.length === 0;
}

function renderTargets(session) {
  ui.targetList.replaceChildren();
  if (!session) {
    ui.emptyState.hidden = false;
    ui.emptyTitle.textContent = viewMode === "history" ? "暂无网页记录" : "当前网页尚无记录";
    ui.emptyDescription.textContent = viewMode === "history" ? "开始记录后打开网页，即可在这里选择并查看。" : "开始记录后打开或刷新网页，请求会显示在这里。";
    renderSelection(null);
    return;
  }
  const groups = aggregate(session.requests);
  const query = ui.searchInput.value.trim().toLowerCase();
  const visible = groups.filter((group) => group.host.includes(query));
  const sort = ui.sortSelect.value;
  visible.sort((a, b) => {
    if (sort === "host") return a.host.localeCompare(b.host);
    if (sort === "size") return b.knownSize - a.knownSize || a.host.localeCompare(b.host);
    if (sort === "count") return b.requests.length - a.requests.length || a.host.localeCompare(b.host);
    return b.slowest - a.slowest || a.host.localeCompare(b.host);
  });
  for (const group of visible) ui.targetList.append(renderGroup(group, session));
  ui.emptyState.hidden = visible.length > 0;
  if (!visible.length) {
    ui.emptyTitle.textContent = query ? "没有匹配目标" : "尚无请求";
    ui.emptyDescription.textContent = query ? "试试其他域名或 IP。" : session.endedAt ? "这个网页没有观察到请求。" : "刷新页面可捕获首次加载；之后的新请求会实时出现。";
  }
  renderSelection(session);
}

function render() {
  const active = Boolean(state.recording);
  ui.tabName.textContent = activeTab?.title || hostFromUrl(activeTab?.url) || "未找到标签页";
  try {
    const url = new URL(activeTab?.url);
    ui.tabUrl.textContent = `${url.host}${url.pathname}`;
  } catch { ui.tabUrl.textContent = activeTab?.url || ""; }
  ui.recordButton.textContent = active ? "停止记录" : "开始记录";
  ui.recordButton.classList.toggle("is-stop", active);
  ui.recordState.textContent = active ? "记录中" : "待命";
  ui.recordState.classList.toggle("is-recording", active);
  ui.refreshHint.textContent = active ? "正在监听各标签页，直到停止。" : "开始后打开网页，即可记录其加载请求。";

  ui.currentView.classList.toggle("is-active", viewMode === "current");
  ui.historyView.classList.toggle("is-active", viewMode === "history");
  ui.currentView.setAttribute("aria-selected", String(viewMode === "current"));
  ui.historyView.setAttribute("aria-selected", String(viewMode === "history"));
  ui.historyControls.hidden = viewMode !== "history";
  ui.historySelect.replaceChildren();
  for (const page of state.pages) {
    const option = node("option", "", `${page.title} · ${page.pageUrl} · ${new Date(page.startedAt).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`);
    option.value = page.id;
    ui.historySelect.append(option);
  }
  const session = viewedSession();
  if (viewMode === "history" && session) {
    historyId = session.id;
    ui.historySelect.value = session.id;
  }
  ui.historySelect.disabled = state.pages.length === 0;
  ui.sessionHeading.hidden = !session;
  ui.sessionCaption.textContent = session ? `${session.pageUrl} · ${session.requests.length} 条请求${session.droppedCount ? ` · 已丢弃 ${session.droppedCount} 条` : ""}` : "";
  ui.metricRequests.textContent = String(session?.requests.length || 0);
  const groups = session ? aggregate(session.requests) : [];
  ui.metricTargets.textContent = String(groups.length);
  const known = session?.requests.filter((item) => item.sizeBytes !== null) || [];
  ui.metricSize.textContent = known.length ? formatBytes(known.reduce((sum, item) => sum + item.sizeBytes, 0)) : "—";
  renderTargets(session);
}

async function copyText(value) {
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    showNotice(value.includes("\n") ? "多行规则已复制。" : "候选规则已复制。");
  } catch (error) { showNotice(`复制失败：${error.message}`, true); }
}

ui.recordButton.addEventListener("click", async () => {
  ui.recordButton.disabled = true;
  try {
    if (state.recording) {
      await send({ type: "STOP" });
      showNotice("已停止记录；结果仍可在“已记录网页”中查看。");
    } else {
      const granted = await chrome.permissions.request({ origins: ["<all_urls>"] });
      if (!granted) throw new Error("未授予网站访问权限，无法记录第三方资源。");
      await send({ type: "START", tabId: activeTab?.id });
      viewMode = "current";
      showNotice("已开始监听。之后打开的网页会自动记录；当前网页可刷新后捕获首次加载。");
    }
    await refresh();
  } catch (error) { showNotice(error.message, true); }
  finally { ui.recordButton.disabled = false; }
});
ui.currentView.addEventListener("click", () => { viewMode = "current"; render(); });
ui.historyView.addEventListener("click", () => { viewMode = "history"; render(); });
ui.historySelect.addEventListener("change", () => { historyId = ui.historySelect.value; render(); });
ui.searchInput.addEventListener("input", () => renderTargets(viewedSession()));
ui.sortSelect.addEventListener("change", () => renderTargets(viewedSession()));
ui.clearButton.addEventListener("click", async () => {
  const session = viewedSession();
  if (!session) return;
  try {
    await send({ type: "CLEAR_PAGE", pageId: session.id });
    chosenRules.clear(); selectedRules.clear(); expandedTargets = new Set();
    showNotice("已清除此网页记录。");
    await refresh();
  } catch (error) { showNotice(error.message, true); }
});
ui.copySelected.addEventListener("click", () => {
  const session = viewedSession();
  if (!session) return;
  const lines = [...selectedRules.entries()].filter(([key]) => key.startsWith(`${session.id}\n`)).map(([, value]) => value);
  void copyText(uniqueRuleLines(lines));
});

chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "session" && changes.resourceRecorderState) scheduleRefresh();
});
chrome.tabs.onActivated.addListener(() => { scheduleRefresh(); });
chrome.tabs.onUpdated.addListener((tabId) => { if (tabId === activeTab?.id) scheduleRefresh(); });
chrome.windows.onFocusChanged.addListener(() => {
  void readActiveTab().then((tab) => {
    scheduleRefresh();
  }).catch((error) => showNotice(error.message, true));
});

void refresh().catch((error) => showNotice(error.message, true));
