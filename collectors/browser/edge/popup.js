const DEFAULT_DAEMON = "http://127.0.0.1:6060";

const SOURCE_IDS = ["browser", "shell", "claude", "os"];

document.addEventListener("DOMContentLoaded", async () => {
  await refresh();

  document.getElementById("options").addEventListener("click", () => {
    chrome.runtime.openOptionsPage();
  });

  document.getElementById("refresh").addEventListener("click", refresh);
});

async function refresh() {
  const { daemonUrl = DEFAULT_DAEMON } = await chrome.storage.sync.get(["daemonUrl"]);
  const dot = document.getElementById("statusDot");
  const line = document.getElementById("statusLine");
  const btn = document.getElementById("refresh");

  // disable button while loading
  btn.disabled = true;

  try {
    const resp = await fetch(`${trimSlash(daemonUrl)}/api/v1/health`, {
      signal: AbortSignal.timeout(4000),
    });
    if (!resp.ok) throw new Error(String(resp.status));
    const health = await resp.json();

    dot.classList.remove("offline");
    line.textContent = "Daemon online";

    document.getElementById("statEvents").textContent = formatNum(health.events_stored ?? 0);
    document.getElementById("statUptime").textContent = formatUptime(health.uptime_seconds ?? 0);

    // count events per source
    const counts = { browser: 0, shell: 0, claude: 0, os: 0, other: 0 };
    try {
      const evResp = await fetch(`${trimSlash(daemonUrl)}/api/v1/events?limit=200`, {
        signal: AbortSignal.timeout(4000),
      });
      if (evResp.ok) {
        const evData = await evResp.json();
        const srcs = new Set();
        for (const e of evData.events ?? []) {
          if (SOURCE_IDS.includes(e.source)) {
            counts[e.source]++;
            srcs.add(e.source);
          } else {
            counts.other++;
          }
        }
        document.getElementById("statSources").textContent = srcs.size;
      }
    } catch { /* non-fatal */ }

    for (const src of SOURCE_IDS) {
      const el = document.getElementById(`cnt${capitalize(src)}`);
      if (el) el.textContent = counts[src] > 0 ? counts[src] : "—";
    }
  } catch (err) {
    dot.classList.add("offline");
    line.textContent = "Daemon offline";
    document.getElementById("statEvents").textContent = "—";
    document.getElementById("statSources").textContent = "—";
    document.getElementById("statUptime").textContent = "—";
    for (const src of SOURCE_IDS) {
      const el = document.getElementById(`cnt${capitalize(src)}`);
      if (el) el.textContent = "—";
    }
  }

  btn.disabled = false;
}

function trimSlash(value) {
  return String(value || "").replace(/\/+$/, "");
}

function capitalize(s) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function formatNum(n) {
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function formatUptime(secs) {
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h`;
  return `${Math.floor(secs / 86400)}d`;
}