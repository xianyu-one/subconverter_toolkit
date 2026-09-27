import test from "node:test";
import assert from "node:assert/strict";
import { beginRequest, clearPage, closeTab, completeRequest, currentPage, emptyState, MAX_PAGES, redirectRequest, startRecording, stopRecording, visitPage } from "../src/model.js";

test("recording spans pages and tabs until stopped", () => {
  const state = emptyState();
  startRecording(state, 100);
  visitPage(state, 1, "https://site.example/one", "One", 101);
  beginRequest(state, { tabId: 1, requestId: "one", url: "https://cdn.example/one.js", type: "script", timeStamp: 110 });
  completeRequest(state, { requestId: "one", timeStamp: 120 });
  beginRequest(state, { tabId: 1, requestId: "two", url: "https://site.example/two", type: "main_frame", timeStamp: 130 });
  completeRequest(state, { requestId: "two", timeStamp: 140 });
  beginRequest(state, { tabId: 2, requestId: "three", url: "https://another.example/", type: "main_frame", timeStamp: 150 });
  completeRequest(state, { requestId: "three", timeStamp: 160 });
  assert.equal(state.pages.length, 3);
  assert.equal(state.pages.find((page) => page.pageUrl === "https://site.example/one").requests.length, 1);
  assert.equal(currentPage(state, 1).pageUrl, "https://site.example/two");
  assert.equal(currentPage(state, 2).requests.length, 1);
  closeTab(state, 1, 170);
  assert.ok(state.recording);
  stopRecording(state, 180);
  assert.equal(state.recording, null);
  assert.equal(beginRequest(state, { tabId: 2, requestId: "late", url: "https://another.example/later", type: "image", timeStamp: 190 }), false);
});

test("hash and refresh stay on one page; route changes create another", () => {
  const state = emptyState();
  startRecording(state, 100);
  visitPage(state, 2, "https://site.example/page#one", "Page", 101);
  visitPage(state, 2, "https://site.example/page#two", "Page", 102);
  beginRequest(state, { tabId: 2, requestId: "doc", url: "https://site.example/page", type: "main_frame", timeStamp: 110 });
  visitPage(state, 2, "https://site.example/other", "Other", 120);
  assert.equal(state.pages.length, 2);
  assert.equal(currentPage(state, 2).title, "Other");
});

test("clearing a live page lets later requests continue recording", () => {
  const state = emptyState();
  startRecording(state, 100);
  const page = visitPage(state, 1, "https://site.example/", "Page", 101);
  beginRequest(state, { tabId: 1, requestId: "one", url: "https://cdn.example/1", type: "image", timeStamp: 110 });
  completeRequest(state, { requestId: "one", timeStamp: 120 });
  clearPage(state, page.id);
  beginRequest(state, { tabId: 1, requestId: "two", url: "https://cdn.example/2", type: "image", timeStamp: 130 });
  completeRequest(state, { requestId: "two", timeStamp: 140 });
  assert.deepEqual(page.requests.map((request) => request.url), ["https://cdn.example/2"]);
});

test("a later recording keeps earlier pages and creates a fresh visit", () => {
  const state = emptyState();
  startRecording(state, 100);
  const first = visitPage(state, 1, "https://site.example/", "First", 101);
  stopRecording(state, 120);
  startRecording(state, 130);
  const second = visitPage(state, 1, "https://site.example/", "Second", 131);
  assert.notEqual(first.id, second.id);
  assert.equal(state.pages.length, 2);
  assert.equal(currentPage(state, 1), second);
});

test("redirects retain their chain and actual connection details", () => {
  const state = emptyState();
  startRecording(state, 100);
  visitPage(state, 1, "https://site.example/", "Page", 101);
  beginRequest(state, { tabId: 1, requestId: "r1", url: "https://a.example/start", type: "xmlhttprequest", timeStamp: 110 });
  redirectRequest(state, { requestId: "r1", timeStamp: 130, statusCode: 302, redirectUrl: "https://b.example/final", responseHeaders: [{ name: "Content-Length", value: "0" }] });
  beginRequest(state, { tabId: 1, requestId: "r1", url: "https://b.example/final", type: "xmlhttprequest", timeStamp: 132 });
  completeRequest(state, { requestId: "r1", timeStamp: 170, statusCode: 200, ip: "203.0.113.5", responseHeaders: [{ name: "content-length", value: "512" }] });
  const requests = state.pages[0].requests;
  assert.equal(requests.length, 2);
  assert.equal(requests[0].redirectTo, "https://b.example/final");
  assert.equal(requests[1].redirectFrom, requests[0].id);
  assert.equal(requests[1].sizeBytes, 512);
  assert.equal(requests[1].ip, "203.0.113.5");
});

test("limits retain recent pages and requests", () => {
  const state = emptyState();
  startRecording(state, 100);
  for (let index = 0; index <= MAX_PAGES; index += 1) visitPage(state, index + 1, `https://site${index}.example/`, null, index + 101);
  assert.equal(state.pages.length, MAX_PAGES);
  assert.equal(state.removedPages, 1);
  const page = currentPage(state, MAX_PAGES + 1);
  for (let index = 0; index < 1001; index += 1) {
    beginRequest(state, { tabId: page.tabId, requestId: `request-${index}`, url: `https://cdn.example/${index}`, type: "image", timeStamp: index + 200 });
    completeRequest(state, { requestId: `request-${index}`, timeStamp: index + 201 });
  }
  assert.equal(page.requests.length, 1000);
  assert.equal(page.droppedCount, 1);
});
