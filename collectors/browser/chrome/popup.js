const DEFAULT_DAEMON = "http://127.0.0.1:6060";

document.addEventListener("DOMContentLoaded", async () => {
  const { daemonUrl = DEFAULT_DAEMON } = await chrome.storage.sync.get(["daemonUrl"]);
  const status = document.getElementById("status");
  try {
    const resp = await fetch(`${trimSlash(daemonUrl)}/api/v1/health`);
    if (!resp.ok) throw new Error(String(resp.status));
    const health = await resp.json();
    status.textContent = `Daemon OK · ${health.events_stored ?? 0} events`;
  } catch {
    status.textContent = "Daemon not reachable";
  }
});

document.getElementById("options").addEventListener("click", () => {
  chrome.runtime.openOptionsPage();
});

function trimSlash(value) {
  return String(value || "").replace(/\/+$/, "");
}
