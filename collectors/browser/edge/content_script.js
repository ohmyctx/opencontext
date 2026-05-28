const MAX_TEXT_CHARS = 500;
const SEARCH_INPUT_TYPES = new Set(["search"]);
const TEXT_INPUT_TYPES = new Set(["text", "search", "url", "email", "tel"]);
const SENSITIVE_INPUT_TYPES = new Set(["password", "number", "date", "datetime-local", "month", "week", "time"]);
const SUBMIT_TEXT_PATTERN = /\b(send|submit|post|comment|ask|search)\b|发送|提交|发布|提问|搜索/i;

const lastActionAt = new Map();
let lastEditableElement = null;
let lastEditableAt = 0;

document.addEventListener("focusin", (event) => {
  const target = event.target instanceof Element ? event.target : null;
  if (!target) return;
  const editable = editableFrom(target);
  if (editable) {
    lastEditableElement = editable;
    lastEditableAt = Date.now();
  }
}, true);

document.addEventListener("input", (event) => {
  const target = event.target instanceof Element ? event.target : null;
  if (!target) return;
  const editable = editableFrom(target);
  if (editable) {
    lastEditableElement = editable;
    lastEditableAt = Date.now();
  }
}, true);

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
    maybeEmitSubmittedEditableText(button);
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
  if (event.shiftKey || event.altKey || event.ctrlKey || event.metaKey) return;

  const target = event.target instanceof Element ? event.target : null;
  const input = target ? editableFrom(target) : null;
  if (!input) return;

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
  return [...root.querySelectorAll("input, textarea, [contenteditable=''], [contenteditable='true'], [role='textbox']")]
    .map(describeField)
    .filter(Boolean)
    .filter((field) => field.textLen > 0);
}

function describeField(element) {
  const editable = editableFrom(element);
  if (!editable) return null;
  const text = editableText(editable);
  if (!text) return null;
  const inputType = normalizedInputType(editable);
  const truncated = text.length > MAX_TEXT_CHARS;
  return {
    inputType,
    isSearch: isSearchField(editable, inputType),
    fieldName: editable.name || editable.id || editable.getAttribute("data-testid") || undefined,
    placeholder: editable.placeholder || editable.getAttribute("data-placeholder") || editable.getAttribute("aria-placeholder") || undefined,
    text: truncated ? `${text.slice(0, MAX_TEXT_CHARS - 1)}…` : text,
    textLen: text.length,
    truncated,
  };
}

function editableFrom(element) {
  if (isTextInput(element)) return element;
  const editable = element.closest("[contenteditable=''], [contenteditable='true'], [role='textbox']");
  if (!editable) return null;
  if (editable.closest("[aria-hidden='true'], [hidden]")) return null;
  return editable;
}

function isTextInput(element) {
  if (element instanceof HTMLTextAreaElement) return true;
  if (!(element instanceof HTMLInputElement)) return false;
  const type = normalizedInputType(element);
  if (SENSITIVE_INPUT_TYPES.has(type)) return false;
  return TEXT_INPUT_TYPES.has(type);
}

function normalizedInputType(input) {
  if (!(input instanceof HTMLInputElement)) {
    return input.getAttribute("role") === "textbox" ? "textbox" : "contenteditable";
  }
  return String(input.type || "text").toLowerCase();
}

function isSearchField(input, inputType) {
  if (SEARCH_INPUT_TYPES.has(inputType)) return true;
  const haystack = `${input.name || ""} ${input.id || ""} ${input.placeholder || ""} ${input.getAttribute("aria-label") || ""}`.toLowerCase();
  return /\b(search|query|q|keyword|keywords|搜索|查询)\b/.test(haystack);
}

function maybeEmitSubmittedEditableText(button) {
  if (!looksLikeSubmitButton(button)) return;
  if (!lastEditableElement || !document.contains(lastEditableElement)) return;
  if (Date.now() - lastEditableAt > 10 * 60 * 1000) return;

  const field = describeField(lastEditableElement);
  if (!field || field.textLen === 0) return;
  const type = field.isSearch ? "search" : "text_input";
  emitAction(type, {
    action: type,
    element: tagName(lastEditableElement),
    inputType: field.inputType,
    fieldName: field.fieldName,
    placeholder: field.placeholder,
    text: field.text,
    textLen: field.textLen,
    truncated: field.truncated,
    submitButton: shortText(button.innerText || button.getAttribute("aria-label") || button.value || button.title || ""),
  }, { key: `${type}:${location.href}:${field.textLen}:${field.text.slice(0, 32)}` });
}

function looksLikeSubmitButton(button) {
  if (button instanceof HTMLInputElement && button.type === "submit") return true;
  const text = [
    button.innerText,
    button.getAttribute("aria-label"),
    button.getAttribute("title"),
    button.getAttribute("data-testid"),
    button.id,
    button.className,
    button.value,
  ].join(" ");
  return SUBMIT_TEXT_PATTERN.test(text);
}

function editableText(element) {
  const raw = element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement
    ? element.value
    : element.innerText || element.textContent || "";
  return String(raw || "").replace(/\s+/g, " ").trim();
}

function emitAction(type, payload, options = {}) {
  const now = Date.now();
  const key = options.key || type;
  const previous = lastActionAt.get(key) || 0;
  if (now - previous < 250) return;
  lastActionAt.set(key, now);

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
