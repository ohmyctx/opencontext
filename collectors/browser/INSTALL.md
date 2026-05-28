# Browser Collectors

OpenContext tracks browser activity through extensions for Chrome, Firefox, and Edge. All three extensions capture the same event types and are configured through the same options UI.

## Event Types

- `browser.page_visit`: page URL/domain/title and active duration
- `browser.tab_focus`: active tab changes
- `browser.link_click`: explicit link clicks
- `browser.button_click`: explicit button clicks
- `browser.search`: search box submissions
- `browser.form_submit`: form submissions (no raw field values)
- `browser.text_input`: submitted text from `input`, `textarea`, and contenteditable editors

## Privacy Defaults

All capture options are enabled by default at sensitivity L2. Text input is captured only on submit intent (Enter, form submit, or clicking a send/submit/post/search button). Password fields and numeric/date/time fields are never captured. Add sensitive domains to the ignore list in Options.

---

## Install Locally

### Chrome

1. Start OpenContext:
   ```bash
   oc daemon
   ```

2. Prepare the unpacked extension:
   ```bash
   oc collector browser-chrome install
   ```

3. Open `chrome://extensions`, enable **Developer mode**, click **Load unpacked**, and select:
   ```
   ~/.opencontext/collectors/browser/chrome
   ```

4. Open the extension popup and verify the daemon URL is `http://127.0.0.1:6060`, then click **Send Test Event**.

### Firefox

1. Start OpenContext:
   ```bash
   oc daemon
   ```

2. Prepare the unpacked extension:
   ```bash
   oc collector browser-firefox install
   ```

3. Open `about:debugging#/runtime/this-firefox`, click **Load Temporary Add-on**, and select:
   ```
   ~/.opencontext/collectors/browser/firefox/manifest.json
   ```

   (Alternatively: open `about:addons`, click the gear icon → **Install Add-on from file**, and select the `manifest.json` file.)

4. Click the extension icon in the toolbar, verify the daemon URL is `http://127.0.0.1:6060`, and click **Send Test Event**.

### Edge

1. Start OpenContext:
   ```bash
   oc daemon
   ```

2. Prepare the unpacked extension:
   ```bash
   oc collector browser-edge install
   ```

3. Open `edge://extensions`, enable **Developer mode**, click **Load unpacked**, and select:
   ```
   ~/.opencontext/collectors/browser/edge
   ```

4. Open the extension popup and verify the daemon URL is `http://127.0.0.1:6060`, then click **Send Test Event**.

---

## Verify

```bash
oc events --source browser --since 10m
```

## Browser Compatibility

| Browser | Engine | Status |
|---------|--------|--------|
| Chrome | Chromium (MV3) | ✅ Stable |
| Firefox | Gecko (MV3) | ✅ Stable |
| Edge | Chromium (MV3) | ✅ Stable — same codebase as Chrome |
| Safari | WebKit | Not planned — requires a separate Safari App Extension architecture |