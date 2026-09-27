import test from "node:test";
import assert from "node:assert/strict";

test("background listens across tabs until STOP and exposes every visited page", async () => {
  const listeners = {};
  const event = (name) => ({ addListener(callback) { listeners[name] = callback; } });
  const saved = {};
  globalThis.chrome = {
    storage: { session: {
      async get(key) { return { [key]: saved[key] }; },
      async set(values) { Object.assign(saved, values); }
    } },
    sidePanel: { setPanelBehavior: async () => {} },
    webRequest: {
      onBeforeRequest: event("before"), onBeforeRedirect: event("redirect"),
      onCompleted: event("completed"), onErrorOccurred: event("error")
    },
    webNavigation: { onHistoryStateUpdated: event("history"), onCommitted: event("committed") },
    tabs: { onUpdated: event("updated"), onRemoved: event("removed"), async get(id) { return { id, url: "https://first.example/", title: "First" }; } },
    runtime: { onMessage: event("message") }
  };
  try {
    await import("../src/background.js");
    const send = (message) => new Promise((resolve) => listeners.message(message, {}, resolve));
    await send({ type: "START", tabId: 1 });
    listeners.before({ tabId: 1, requestId: "first", url: "https://first.example/a.js", type: "script", timeStamp: 10 });
    listeners.completed({ requestId: "first", timeStamp: 20, statusCode: 200 });
    listeners.before({ tabId: 1, requestId: "second", url: "https://second.example/", type: "main_frame", timeStamp: 30 });
    listeners.completed({ requestId: "second", timeStamp: 40, statusCode: 200 });
    listeners.before({ tabId: 2, requestId: "third", url: "https://third.example/", type: "main_frame", timeStamp: 50 });
    listeners.completed({ requestId: "third", timeStamp: 60, statusCode: 200 });
    listeners.removed(1);
    const { state } = await send({ type: "GET_STATE" });
    assert.equal(state.pages.length, 3);
    assert.deepEqual(state.pages.map((page) => page.requests.length).sort(), [1, 1, 1]);
    assert.ok(state.recording);
    await send({ type: "STOP" });
    listeners.before({ tabId: 2, requestId: "late", url: "https://third.example/later", type: "image", timeStamp: 70 });
    const after = (await send({ type: "GET_STATE" })).state;
    assert.equal(after.recording, null);
    assert.equal(after.pages.find((page) => page.pageUrl === "https://third.example/").requests.length, 1);
    await new Promise((resolve) => setTimeout(resolve, 200));
  } finally {
    delete globalThis.chrome;
  }
});
