import {
  beginRequest,
  clearPage,
  closeTab,
  completeRequest,
  currentPage,
  emptyState,
  failRequest,
  redirectRequest,
  startRecording,
  stopRecording,
  visitPage,
  trimToBudget
} from "./model.js";

const STORAGE_KEY = "resourceRecorderState";
let statePromise = chrome.storage.session.get(STORAGE_KEY).then((result) => {
  const saved = result[STORAGE_KEY];
  return saved?.version === 2 ? saved : emptyState();
});
let queue = Promise.resolve();
let revision = 0;
let savedRevision = 0;
let flushTimer = null;
let persisting = null;

async function persist() {
  if (persisting) return persisting;
  persisting = (async () => {
    const state = await statePromise;
    while (savedRevision !== revision) {
      trimToBudget(state);
      const currentRevision = revision;
      await chrome.storage.session.set({ [STORAGE_KEY]: structuredClone(state) });
      savedRevision = currentRevision;
    }
  })();
  try {
    await persisting;
  } finally {
    persisting = null;
  }
}

function schedulePersist() {
  if (flushTimer) return;
  flushTimer = setTimeout(() => {
    flushTimer = null;
    void persist().catch((error) => console.error("Resource Recorder storage:", error));
  }, 150);
}

function mutate(change, immediate = false) {
  const task = queue.then(async () => {
    const state = await statePromise;
    const result = change(state);
    if (result !== false) {
      revision += 1;
      if (immediate) await persist();
      else schedulePersist();
    }
    return result;
  });
  queue = task.catch((error) => console.error("Resource Recorder:", error));
  return task;
}

async function snapshot() {
  await queue;
  return structuredClone(await statePromise);
}

chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }).catch(console.error);

const filter = { urls: ["<all_urls>"] };
chrome.webRequest.onBeforeRequest.addListener((details) => {
  void mutate((state) => beginRequest(state, details));
}, filter);
chrome.webRequest.onBeforeRedirect.addListener((details) => {
  void mutate((state) => redirectRequest(state, details));
}, filter, ["responseHeaders"]);
chrome.webRequest.onCompleted.addListener((details) => {
  void mutate((state) => completeRequest(state, details));
}, filter, ["responseHeaders"]);
chrome.webRequest.onErrorOccurred.addListener((details) => {
  void mutate((state) => failRequest(state, details));
}, filter);

chrome.webNavigation.onHistoryStateUpdated.addListener((details) => {
  if (details.frameId === 0) void mutate((state) => Boolean(visitPage(state, details.tabId, details.url, null, details.timeStamp)));
});
chrome.webNavigation.onCommitted.addListener((details) => {
  if (details.frameId === 0) void mutate((state) => Boolean(visitPage(state, details.tabId, details.url, null, details.timeStamp)));
});
chrome.tabs.onUpdated.addListener((tabId, changeInfo) => {
  if (changeInfo.url) void mutate((state) => Boolean(visitPage(state, tabId, changeInfo.url)));
  if (changeInfo.title) void mutate((state) => {
    const page = currentPage(state, tabId);
    if (!page || page.title === changeInfo.title) return false;
    page.title = changeInfo.title;
    return true;
  });
});
chrome.tabs.onRemoved.addListener((tabId) => {
  void mutate((state) => closeTab(state, tabId));
});

chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (!message || typeof message !== "object") return false;
  const handle = async () => {
    if (message.type === "GET_STATE") return { state: await snapshot() };
    if (message.type === "START") {
      const tab = message.tabId ? await chrome.tabs.get(message.tabId).catch(() => null) : null;
      await mutate((state) => {
        if (!startRecording(state)) return false;
        if (tab) visitPage(state, tab.id, tab.url, tab.title);
        return true;
      }, true);
      return { state: await snapshot() };
    }
    if (message.type === "STOP") {
      await mutate((state) => stopRecording(state), true);
      return { state: await snapshot() };
    }
    if (message.type === "CLEAR_PAGE") {
      await mutate((state) => clearPage(state, message.pageId), true);
      return { state: await snapshot() };
    }
    return { error: "未知操作。" };
  };
  handle().then(respond, (error) => respond({ error: error.message || String(error) }));
  return true;
});
