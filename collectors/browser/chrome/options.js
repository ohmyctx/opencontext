const DEFAULT_SETTINGS = {
  daemonUrl: "http://127.0.0.1:6060",
  browserName: "chrome",
  capturePageVisits: true,
  captureTabFocus: true,
  captureActions: true,
  captureInputs: false,
  maxSensitivity: 2,
  ignoredDomains: [],
};

const ids = Object.keys(DEFAULT_SETTINGS);

document.addEventListener("DOMContentLoaded", async () => {
  const settings = { ...DEFAULT_SETTINGS, ...(await chrome.storage.sync.get(ids)) };
  for (const [key, value] of Object.entries(settings)) {
    const el = document.getElementById(key);
    if (!el) continue;
    if (el.type === "checkbox") {
      el.checked = Boolean(value);
    } else if (key === "ignoredDomains") {
      el.value = (value || []).join("\n");
    } else {
      el.value = String(value);
    }
  }
});

document.getElementById("save").addEventListener("click", async () => {
  await chrome.storage.sync.set(readSettings());
  setStatus("Saved");
});

document.getElementById("test").addEventListener("click", async () => {
  const settings = readSettings();
  await chrome.storage.sync.set(settings);
  const event = {
    ts: Date.now(),
    source: "browser",
    type: "tab_focus",
    sensitivity: 1,
    labels: {
      browser: settings.browserName,
      domain: "opencontext.local",
    },
    payload: {
      title: "OpenContext Browser Collector test",
    },
  };
  try {
    const resp = await fetch(`${trimSlash(settings.daemonUrl)}/api/v1/events`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(event),
    });
    setStatus(resp.ok ? "Test event sent" : `Daemon returned ${resp.status}`);
  } catch (err) {
    setStatus(`Test failed: ${err.message}`);
  }
});

function readSettings() {
  return {
    daemonUrl: document.getElementById("daemonUrl").value || DEFAULT_SETTINGS.daemonUrl,
    browserName: document.getElementById("browserName").value || DEFAULT_SETTINGS.browserName,
    capturePageVisits: document.getElementById("capturePageVisits").checked,
    captureTabFocus: document.getElementById("captureTabFocus").checked,
    captureActions: document.getElementById("captureActions").checked,
    captureInputs: document.getElementById("captureInputs").checked,
    maxSensitivity: Number(document.getElementById("maxSensitivity").value || 2),
    ignoredDomains: document.getElementById("ignoredDomains").value
      .split(/\r?\n/)
      .map((line) => line.trim().toLowerCase())
      .filter(Boolean),
  };
}

function trimSlash(value) {
  return String(value || "").replace(/\/+$/, "");
}

function setStatus(message) {
  const el = document.getElementById("status");
  el.textContent = message;
  setTimeout(() => {
    if (el.textContent === message) el.textContent = "";
  }, 2500);
}
