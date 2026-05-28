# OpenContext Browser Collectors

The first browser collector is a Chrome Manifest V3 extension under `collectors/browser/chrome`.

It captures:

- `browser.page_visit`: page URL/domain/title and active duration.
- `browser.tab_focus`: active tab changes.
- `browser.link_click`: explicit link clicks.
- `browser.button_click`: explicit button clicks.
- `browser.search`: search box submissions.
- `browser.form_submit`: form submissions without raw field values.
- `browser.text_input`: submitted text input content, only when enabled in options.

## Privacy Defaults

Page visits, tab focus, link clicks, button clicks, form submits, and searches are enabled by default with max sensitivity L2.

Raw submitted text input content is disabled by default. Enable it from the extension options only when the user explicitly wants that capture.

The content script never reads password fields or numeric/date/time fields. Ignored domains can be configured in the options page.

## Install Locally In Chrome

1. Start OpenContext:

   ```bash
   oc daemon
   ```

2. Open `chrome://extensions`.
3. Enable Developer mode.
4. Click "Load unpacked".
5. Select:

   ```text
   collectors/browser/chrome
   ```

6. Open the extension options and verify the daemon URL:

   ```text
   http://127.0.0.1:6060
   ```

7. Click "Send Test Event".

Verify:

```bash
oc events --source browser --since 10m
```

## Browser Compatibility Plan

Chrome and Edge can use the Chrome MV3 collector with minimal packaging changes.

Firefox should be a separate package/manifest under `collectors/browser/firefox` because extension background configuration and review expectations differ. Keep the event payload builder and content script semantics aligned across browsers.
