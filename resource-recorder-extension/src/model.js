import { hostFromUrl, pageIdentity } from "./rules.js";

export const MAX_PAGES = 50;
export const MAX_REQUESTS = 1000;
export const MAX_STATE_CHARS = 3_500_000;

export function emptyState() {
  return { version: 2, recording: null, pages: [], pending: {}, removedPages: 0 };
}

export function currentPage(state, tabId) {
  if (!state.recording) return null;
  return state.pages.find((page) => page.tabId === tabId && page.recordingId === state.recording.id && !page.endedAt) || null;
}

export function startRecording(state, now = Date.now()) {
  if (state.recording) return false;
  state.recording = { id: `${now}-${Math.random().toString(36).slice(2, 8)}`, startedAt: now };
  return true;
}

export function stopRecording(state, now = Date.now()) {
  if (!state.recording) return false;
  for (const page of state.pages) {
    if (page.recordingId === state.recording.id && !page.endedAt) page.endedAt = now;
  }
  state.recording = null;
  state.pending = {};
  return true;
}

export function visitPage(state, tabId, pageUrl, title, now = Date.now()) {
  if (!state.recording) return false;
  const identity = pageIdentity(pageUrl);
  const existing = currentPage(state, tabId);
  if (existing?.pageUrl === identity) {
    if (title) existing.title = title;
    return existing;
  }
  if (existing) existing.endedAt = now;
  if (!identity || !/^https?:/.test(identity)) return false;
  if (state.pages.length >= MAX_PAGES) {
    clearPage(state, state.pages[state.pages.length - 1].id, false);
    state.removedPages += 1;
  }
  const page = {
    id: `${tabId}-${now}-${Math.random().toString(36).slice(2, 8)}`,
    recordingId: state.recording.id,
    tabId,
    pageUrl: identity,
    title: title || hostFromUrl(identity) || identity,
    startedAt: now,
    endedAt: null,
    requests: [],
    droppedCount: 0
  };
  state.pages.unshift(page);
  return page;
}

export function closeTab(state, tabId, now = Date.now()) {
  const page = currentPage(state, tabId);
  if (!page) return false;
  page.endedAt = now;
  return true;
}

export function clearPage(state, id, preserveActive = true) {
  const index = state.pages.findIndex((page) => page.id === id);
  if (index < 0) return false;
  const page = state.pages[index];
  if (preserveActive && state.recording && page.recordingId === state.recording.id && !page.endedAt) {
    page.requests = [];
    page.droppedCount = 0;
  } else {
    state.pages.splice(index, 1);
  }
  for (const [requestId, pending] of Object.entries(state.pending)) {
    if (pending.pageId === id) delete state.pending[requestId];
  }
  return true;
}

export function beginRequest(state, details) {
  if (details.tabId < 0 || !hostFromUrl(details.url)) return false;
  if (details.type === "main_frame") visitPage(state, details.tabId, details.url, null, details.timeStamp);
  const page = currentPage(state, details.tabId);
  if (!page) return false;
  const previous = state.pending[details.requestId];
  if (previous?.pageId === page.id && previous.url === details.url) {
    previous.startedAt = details.timeStamp;
    return true;
  }
  state.pending[details.requestId] = {
    pageId: page.id,
    tabId: details.tabId,
    url: details.url,
    type: details.type,
    startedAt: details.timeStamp,
    hop: previous?.pageId === page.id ? previous.hop + 1 : 0,
    redirectFrom: previous?.redirectFrom || null
  };
  return true;
}

function declaredSize(headers) {
  const header = headers?.find((item) => item.name.toLowerCase() === "content-length");
  if (!header || !/^\d+$/.test(header.value || "")) return null;
  const value = Number(header.value);
  return Number.isSafeInteger(value) ? value : null;
}

function finishPending(state, details, kind) {
  const pending = state.pending[details.requestId];
  if (!pending) return null;
  const page = state.pages.find((item) => item.id === pending.pageId);
  if (!page) {
    delete state.pending[details.requestId];
    return null;
  }
  const id = `${details.requestId}:${pending.hop}`;
  const record = {
    id,
    url: pending.url,
    type: pending.type,
    startedAt: pending.startedAt,
    endedAt: details.timeStamp,
    durationMs: Math.max(0, details.timeStamp - pending.startedAt),
    statusCode: details.statusCode ?? null,
    status: kind,
    error: details.error || null,
    sizeBytes: declaredSize(details.responseHeaders),
    ip: details.ip || null,
    fromCache: Boolean(details.fromCache),
    redirectTo: kind === "redirect" ? details.redirectUrl : null,
    redirectFrom: pending.redirectFrom
  };
  page.requests.push(record);
  if (page.requests.length > MAX_REQUESTS) {
    page.requests.shift();
    page.droppedCount += 1;
  }
  delete state.pending[details.requestId];
  return record;
}

export function redirectRequest(state, details) {
  const pending = state.pending[details.requestId];
  if (!pending) return false;
  const record = finishPending(state, details, "redirect");
  if (!record || !hostFromUrl(details.redirectUrl)) return Boolean(record);
  const page = pending.type === "main_frame"
    ? visitPage(state, pending.tabId, details.redirectUrl, null, details.timeStamp)
    : state.pages.find((item) => item.id === pending.pageId);
  if (!page) return true;
  state.pending[details.requestId] = {
    pageId: page.id,
    tabId: pending.tabId,
    url: details.redirectUrl,
    type: pending.type,
    startedAt: details.timeStamp,
    hop: pending.hop + 1,
    redirectFrom: record.id
  };
  return true;
}

export function completeRequest(state, details) {
  return Boolean(finishPending(state, details, "completed"));
}

export function failRequest(state, details) {
  return Boolean(finishPending(state, details, "error"));
}

export function trimToBudget(state) {
  while (JSON.stringify(state).length > MAX_STATE_CHARS) {
    const candidates = state.pages.filter((page) => page.requests.length);
    if (!candidates.length) break;
    const oldest = candidates.reduce((a, b) => a.requests[0].endedAt <= b.requests[0].endedAt ? a : b);
    oldest.requests.shift();
    oldest.droppedCount += 1;
  }
}
