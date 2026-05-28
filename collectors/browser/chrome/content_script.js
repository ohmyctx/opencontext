const MAX_TEXT_CHARS = 500;
const SEARCH_INPUT_TYPES = new Set(["search"]);
const TEXT_INPUT_TYPES = new Set(["text", "search", "url", "email", "tel"]);
const SENSITIVE_INPUT_TYPES = new Set(["password", "number", "date", "datetime-local", "month", "week", "time"]);

let lastActionAt = 0;

document.addEventListener("click", (event) => {
  const target = event.target instanceof Element ? event.target : null;
  if (!target) return;

  const anchor = target.closest("a[href]");
  if (anchor) {
    emitAction("link_click", {
      action: "link_click",
      element: tagName(anchor),
      text: shortText(anchor.innerText || anchor.getAttribute("aria-label") || anchor.title || ""),
      href: anchor.href,
    });
    return;
  }

  const button = target.closest("button, [role='button'], input[type='button'], input[type='submit']");
  if (button) {
    emitAction("button_click", {
      action: "button_click",
      element: tagName(button),
      text: shortText(button.innerText || button.getAttribute("aria-label") || button.value || button.title || ""),
    });
  }
}, true);

document.addEventListener("submit", (event) => {
  const form = event.target instanceof HTMLFormElement ? event.target : null;
  if (!form) return;

  const fields = collectTextFields(form);
  const search = fields.find((field) => field.isSearch);
  if (search) {
    emitAction("search", {
      action: "search",
      element: "form",
      inputType: search.inputType,
      fieldName: search.fieldName,
      placeholder: search.placeholder,
      text: search.text,
      textLen: search.textLen,
      truncated: search.truncated,
      formAction: form.action || undefined,
    });
    return;
  }

  if (fields.length > 0) {
    emitAction("form_submit", {
      action: "form_submit",
      element: "form",
      text: `${fields.length} text field(s) submitted`,
      textLen: fields.reduce((sum, field) => sum + field.textLen, 0),
      formAction: form.action || undefined,
    });
  }
}, true);

document.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" || event.isComposing) return;
  const input = event.target;
  if (!isTextInput(input)) return;

  const field = describeField(input);
  if (!field || field.textLen === 0) return;
  const type = field.isSearch ? "search" : "text_input";
  emitAction(type, {
    action: type,
    element: tagName(input),
    inputType: field.inputType,
    fieldName: field.fieldName,
    placeholder: field.placeholder,
    text: field.text,
    textLen: field.textLen,
    truncated: field.truncated,
  });
}, true);

function collectTextFields(root) {
  return [...root.querySelectorAll("input, textarea")]
    .map(describeField)
    .filter(Boolean)
    .filter((field) => field.textLen > 0);
}

function describeField(element) {
  if (!isTextInput(element)) return null;
  const text = String(element.value || "").trim();
  if (!text) return null;
  const inputType = normalizedInputType(element);
  const truncated = text.length > MAX_TEXT_CHARS;
  return {
    inputType,
    isSearch: isSearchField(element, inputType),
    fieldName: element.name || element.id || undefined,
    placeholder: element.placeholder || undefined,
    text: truncated ? `${text.slice(0, MAX_TEXT_CHARS - 1)}…` : text,
    textLen: text.length,
    truncated,
  };
}

function isTextInput(element) {
  if (element instanceof HTMLTextAreaElement) return true;
  if (!(element instanceof HTMLInputElement)) return false;
  const type = normalizedInputType(element);
  if (SENSITIVE_INPUT_TYPES.has(type)) return false;
  return TEXT_INPUT_TYPES.has(type);
}

function normalizedInputType(input) {
  return String(input.type || "text").toLowerCase();
}

function isSearchField(input, inputType) {
  if (SEARCH_INPUT_TYPES.has(inputType)) return true;
  const haystack = `${input.name || ""} ${input.id || ""} ${input.placeholder || ""} ${input.getAttribute("aria-label") || ""}`.toLowerCase();
  return /\b(search|query|q|keyword|keywords|搜索|查询)\b/.test(haystack);
}

function emitAction(type, payload) {
  const now = Date.now();
  if (now - lastActionAt < 250) return;
  lastActionAt = now;

  chrome.runtime.sendMessage({
    kind: "opencontext.browser_event",
    type,
    sensitivity: type === "link_click" || type === "button_click" ? 2 : 2,
    url: location.href,
    title: document.title,
    ...payload,
  }).catch(() => {});
}

function tagName(element) {
  return element.tagName.toLowerCase();
}

function shortText(text) {
  const normalized = String(text || "").replace(/\s+/g, " ").trim();
  if (!normalized) return undefined;
  return normalized.length > 160 ? `${normalized.slice(0, 159)}…` : normalized;
}
