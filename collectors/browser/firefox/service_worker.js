const DEFAULT_SETTINGS = {
  daemonUrl: "http://127.0.0.1:6060",
  browserName: "chrome",
  capturePageVisits: true,
  captureTabFocus: true,
  captureActions: true,
  captureInputs: true,
  maxSensitivity: 2,
  minPageDurationMs: 1500,
  ignoredDomains: [],
};

const activeTabs = new Map();

chrome.runtime.onInstalled.addListener(async () => {
  const current = await chrome.storage.sync.get(Object.keys(DEFAULT_SETTINGS));
  await chrome.storage.sync.set({ ...DEFAULT_SETTINGS, ...stripUndefined(current) });
});

chrome.tabs.onActivated.addListener(async ({ tabId }) => {
  const settings = await getSettings();
  await closeOtherActiveTabs(tabId, settings);
  const tab = await safeGetTab(tabId);
  if (!tab || !isTrackableUrl(tab.url) || isIgnored(tab.url, settings)) return;
  activeTabs.set(tabId, snapshotTab(tab));
  if (settings.captureTabFocus) {
    await pushEvent(buildTabFocusEvent(tab, settings));
  }
});

chrome.tabs.onUpdated.addListener(async (tabId, changeInfo, tab) => {
  if (changeInfo.status !== "complete" || !tab.active) return;
  const settings = await getSettings();
  if (!isTrackableUrl(tab.url) || isIgnored(tab.url, settings)) return;
  const previous = activeTabs.get(tabId);
  if (previous && previous.url !== tab.url) {
    await emitPageVisit(previous, settings);
  }
  activeTabs.set(tabId, snapshotTab(tab));
  if (settings.captureTabFocus) {
    await pushEvent(buildTabFocusEvent(tab, settings));
  }
});

chrome.tabs.onRemoved.addListener(async (tabId) => {
  const settings = await getSettings();
  const previous = activeTabs.get(tabId);
  activeTabs.delete(tabId);
  if (previous) {
    await emitPageVisit(previous, settings);
  }
});

chrome.windows.onFocusChanged.addListener(async (windowId) => {
  if (windowId === chrome.windows.WINDOW_ID_NONE) return;
  const settings = await getSettings();
  const tabs = await chrome.tabs.query({ active: true, windowId });
  const tab = tabs[0];
  if (!tab || !isTrackableUrl(tab.url) || isIgnored(tab.url, settings)) return;
  await closeOtherActiveTabs(tab.id, settings);
  activeTabs.set(tab.id, snapshotTab(tab));
  if (settings.captureTabFocus) {
    await pushEvent(buildTabFocusEvent(tab, settings));
  }
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (!message || message.kind !== "opencontext.browser_event") return false;
  handleContentEvent(message, sender)
    .then(() => sendResponse({ ok: true }))
    .catch((err) => sendResponse({ ok: false, error: String(err) }));
  return true;
});

async function handleContentEvent(message, sender) {
  const settings = await getSettings();
  const tab = sender.tab;
  const url = message.url || tab?.url;
  if (!isTrackableUrl(url) || isIgnored(url, settings)) return;

  const sensitivity = Number(message.sensitivity || 2);
  if (sensitivity > settings.maxSensitivity) return;
  if (message.type === "text_input" && !settings.captureInputs) return;
  if (["link_click", "button_click", "form_submit", "search"].includes(message.type) && !settings.captureActions) return;

  await pushEvent({
    ts: Date.now(),
    source: "browser",
    type: message.type,
    sensitivity,
    labels: compact({
      browser: settings.browserName,
      domain: domainOf(url),
      action: message.action,
      element: message.element,
      input_type: message.inputType,
    }),
    payload: compact({
      url: sensitivity >= 2 ? url : undefined,
      title: message.title || tab?.title,
      text: message.text,
      text_len: message.textLen,
      href: message.href,
      form_action: message.formAction,
      field_name: message.fieldName,
      placeholder: message.placeholder,
      submit_button: message.submitButton,
      truncated: message.truncated,
    }),
  }, settings);
}

async function closeOtherActiveTabs(activeTabId, settings) {
  for (const [tabId, snapshot] of [...activeTabs.entries()]) {
    if (tabId === activeTabId) continue;
    activeTabs.delete(tabId);
    await emitPageVisit(snapshot, settings);
  }
}

async function emitPageVisit(snapshot, settings) {
  if (!settings.capturePageVisits || isIgnored(snapshot.url, settings)) return;
  const durationMs = Date.now() - snapshot.startedAt;
  if (durationMs < settings.minPageDurationMs) return;
  await pushEvent(buildPageVisitEvent(snapshot, durationMs, settings), settings);
}

function buildTabFocusEvent(tab, settings) {
  const sensitivity = settings.maxSensitivity >= 2 ? 2 : 1;
  return {
    ts: Date.now(),
    source: "browser",
    type: "tab_focus",
    sensitivity,
    labels: compact({
      browser: settings.browserName,
      domain: domainOf(tab.url),
    }),
    payload: compact({
      title: tab.title,
      url: sensitivity >= 2 ? tab.url : undefined,
      tab_id: String(tab.id),
      window_id: String(tab.windowId),
    }),
  };
}

function buildPageVisitEvent(snapshot, durationMs, settings) {
  const sensitivity = settings.maxSensitivity >= 2 ? 2 : 1;
  return {
    ts: Date.now(),
    source: "browser",
    type: "page_visit",
    sensitivity,
    labels: compact({
      browser: settings.browserName,
      domain: domainOf(snapshot.url),
    }),
    payload: compact({
      title: snapshot.title,
      url: sensitivity >= 2 ? snapshot.url : undefined,
      duration_ms: durationMs,
    }),
  };
}

async function pushEvent(event, settings = null) {
  const cfg = settings || await getSettings();
  if (event.sensitivity > cfg.maxSensitivity) return;
  const response = await fetch(`${trimSlash(cfg.daemonUrl)}/api/v1/events`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(ensureEventShape(event)),
  });
  if (!response.ok) {
    throw new Error(`OpenContext daemon returned ${response.status}`);
  }
}

function ensureEventShape(event) {
  return {
    ...event,
    labels: event.labels || {},
    payload: event.payload || {},
  };
}

async function getSettings() {
  const stored = await chrome.storage.sync.get(Object.keys(DEFAULT_SETTINGS));
  return { ...DEFAULT_SETTINGS, ...stripUndefined(stored) };
}

function stripUndefined(value) {
  return Object.fromEntries(Object.entries(value || {}).filter(([, v]) => v !== undefined));
}

async function safeGetTab(tabId) {
  try {
    return await chrome.tabs.get(tabId);
  } catch {
    return null;
  }
}

function snapshotTab(tab) {
  return {
    url: tab.url,
    title: tab.title,
    startedAt: Date.now(),
  };
}

function isTrackableUrl(url) {
  return /^https?:\/\//i.test(url || "");
}

function isIgnored(url, settings) {
  const domain = domainOf(url);
  return settings.ignoredDomains.some((item) => domain === item || domain.endsWith(`.${item}`));
}

function domainOf(rawUrl) {
  try {
    return new URL(rawUrl).hostname;
  } catch {
    return "";
  }
}

function trimSlash(value) {
  return String(value || "").replace(/\/+$/, "");
}

function compact(obj) {
  return Object.fromEntries(Object.entries(obj).filter(([, v]) => v !== undefined && v !== null && v !== ""));
}
