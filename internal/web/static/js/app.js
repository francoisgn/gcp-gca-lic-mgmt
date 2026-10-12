/* =============================================================================
   Shared helpers (no framework, no build step).
   - GCA.api(): JSON calls with timeout and uniform errors
   - GCA.el(): safe DOM builder (textContent only, never innerHTML with data)
   - GCA.toast(), GCA.confirm(): feedback and confirmation dialog
   ============================================================================= */
'use strict';

const GCA = (() => {
  /** Data passed by the server in <script type="application/json" id="page-data">. */
  function pageData() {
    const node = document.getElementById('page-data');
    if (!node) return {};
    try { return JSON.parse(node.textContent); } catch { return {}; }
  }

  /**
   * Calls the JSON API. Rejects with an Error carrying `status` (HTTP) or
   * `timeout` (client-side abort) so callers can display the right state.
   */
  async function api(method, url, body, { timeoutMs } = {}) {
    const ctrl = new AbortController();
    const timer = timeoutMs ? setTimeout(() => ctrl.abort(), timeoutMs) : null;
    try {
      const res = await fetch(url, {
        method,
        credentials: 'same-origin',
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal: ctrl.signal,
      });
      let data = null;
      try { data = await res.json(); } catch { /* empty body */ }
      if (!res.ok) {
        const err = new Error((data && data.error) || `Unexpected error (HTTP ${res.status})`);
        err.status = res.status;
        throw err;
      }
      return data;
    } catch (e) {
      if (e.name === 'AbortError') {
        const err = new Error('The server is taking longer than expected.');
        err.timeout = true;
        throw err;
      }
      throw e;
    } finally {
      if (timer) clearTimeout(timer);
    }
  }

  /**
   * Builds a DOM element. attrs: class, dataset (object), on<Event> handlers,
   * any other attribute. Children: strings (text nodes) or nodes.
   */
  function el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') node.className = v;
      else if (k === 'dataset') Object.assign(node.dataset, v);
      else if (k.startsWith('on') && typeof v === 'function') node.addEventListener(k.slice(2), v);
      else node.setAttribute(k, v === true ? '' : v);
    }
    for (const c of children.flat()) {
      if (c === undefined || c === null || c === false) continue;
      node.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return node;
  }

  /** Inline SVG icons (same set as the server-side icon helper). */
  const ICONS = {
    check: '<path d="M5 12.5l4.2 4.2L19 7"/>',
    x: '<path d="M6 6l12 12M18 6L6 18"/>',
    minus: '<path d="M6 12h12"/>',
    clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>',
    alert: '<path d="M12 3.5l9.5 16.5h-19z"/><path d="M12 10v4.5M12 17.5v.01"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.5v.01"/>',
    ok: '<circle cx="12" cy="12" r="9"/><path d="M8 12.3l2.8 2.8L16.5 9.5"/>',
    queue: '<path d="M4 6h16M4 12h10M4 18h6"/><circle cx="18.5" cy="16.5" r="2.5"/>',
  };
  function icon(name) {
    const span = document.createElement('span');
    span.className = 'alert__icon';
    span.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name] || ''}</svg>`;
    return span;
  }

  /** Alert box: tone = ok | warn | ko | info. */
  function alertBox(tone, title, ...content) {
    const iconName = { ok: 'ok', warn: 'clock', ko: 'x', info: 'info' }[tone] || 'info';
    return el('div', { class: `alert alert--${tone}`, role: tone === 'ko' ? 'alert' : 'status' },
      icon(iconName),
      el('div', {}, el('div', { class: 'alert__title' }, title), ...content));
  }

  function toast(message, kind = 'info', ms = 5000) {
    const box = document.getElementById('toasts');
    if (!box) return;
    const t = el('div', { class: `toast toast--${kind}` }, message);
    box.append(t);
    setTimeout(() => t.remove(), ms);
  }

  /**
   * Confirmation dialog. Resolves { ok, value } (value = textarea content
   * when `input` is true).
   */
  function confirm({ title, text = '', confirmLabel = 'Confirm', input = false, inputLabel = 'Reason', danger = false }) {
    const dlg = document.getElementById('dialog');
    dlg.querySelector('#dialog-title').textContent = title;
    dlg.querySelector('#dialog-text').textContent = text;
    const wrap = dlg.querySelector('#dialog-input-wrap');
    const field = dlg.querySelector('#dialog-input');
    wrap.hidden = !input;
    field.value = '';
    field.required = input;
    dlg.querySelector('#dialog-input-label').textContent = inputLabel;
    const btn = dlg.querySelector('#dialog-confirm');
    btn.textContent = confirmLabel;
    btn.className = danger ? 'btn btn--danger' : 'btn btn--primary';
    return new Promise((resolve) => {
      dlg.addEventListener('close', () => {
        const ok = dlg.returnValue === 'confirm' && (!input || field.value.trim() !== '');
        resolve({ ok, value: field.value.trim() });
      }, { once: true });
      dlg.showModal();
      (input ? field : btn).focus();
    });
  }

  const fmt = new Intl.DateTimeFormat('en-GB', { day: '2-digit', month: 'short', year: 'numeric' });
  const fmtTime = new Intl.DateTimeFormat('en-GB', { day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  const date = (iso) => (iso ? fmt.format(new Date(iso)) : '—');
  const dateTime = (iso) => (iso ? fmtTime.format(new Date(iso)) : '—');
  function relative(iso) {
    if (!iso) return '';
    const diff = new Date(iso).getTime() - Date.now();
    const abs = Math.abs(diff), past = diff < 0;
    const unit = abs < 3.6e6 ? ['minute', 6e4] : abs < 1.728e8 ? ['hour', 3.6e6] : ['day', 8.64e7];
    const n = Math.max(1, Math.round(abs / unit[1]));
    const s = `${n} ${unit[0]}${n > 1 ? 's' : ''}`;
    return past ? `${s} ago` : `in ${s}`;
  }

  /** Makes a group of chips behave like radio buttons. */
  function radioChips(group, onSelect) {
    group.addEventListener('click', (e) => {
      const chip = e.target.closest('.chip');
      if (!chip || chip.disabled) return;
      group.querySelectorAll('.chip').forEach((c) => c.setAttribute('aria-pressed', String(c === chip)));
      onSelect(chip);
    });
  }

  /** Disables a button and shows a spinner while `fn` runs. */
  async function busy(btn, fn) {
    const label = btn.innerHTML;
    btn.disabled = true;
    btn.classList.add('is-loading');
    btn.insertAdjacentHTML('afterbegin', '<span class="spinner" aria-hidden="true"></span>');
    try { return await fn(); } finally {
      btn.disabled = false;
      btn.classList.remove('is-loading');
      btn.innerHTML = label;
    }
  }

  // Dev identity switcher: come back to the current page after switching.
  document.querySelectorAll('.devbar input[name="back"]').forEach((i) => { i.value = location.pathname + location.search; });

  return { pageData, api, el, icon, alertBox, toast, confirm, date, dateTime, relative, radioChips, busy };
})();
