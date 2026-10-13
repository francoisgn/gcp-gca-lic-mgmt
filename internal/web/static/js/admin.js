/* =============================================================================
   Administration page: tabs, approvals, waiting list, buffer, assign/transfer.
   ============================================================================= */
'use strict';

(() => {
  const { api, toast, confirm, busy } = GCA;

  // Tabs (remembered in the URL hash so a reload keeps the tab).
  const tabs = [...document.querySelectorAll('.tab')];
  function show(name) {
    tabs.forEach((t) => t.setAttribute('aria-selected', String(t.dataset.tab === name)));
    document.querySelectorAll('[data-panel]').forEach((p) => { p.hidden = p.dataset.panel !== name; });
    history.replaceState(null, '', `#${name}`);
  }
  tabs.forEach((t) => t.addEventListener('click', () => show(t.dataset.tab)));
  const initial = location.hash.slice(1);
  if (tabs.some((t) => t.dataset.tab === initial)) show(initial);

  /** Human message for a fulfilment result. */
  function outcomeMessage(res, who) {
    switch (res.outcome) {
      case 'ASSIGNED': return [`License assigned to ${who}.`, 'ok'];
      case 'QUEUED': return [`Pool exhausted: ${who} is #${res.queuePosition} in the waiting list.`, 'warn'];
      case 'BUFFERED': return [`Google timed out: ${who} is in the buffer, retried automatically.`, 'warn'];
      default: return [res.message || 'Assignment failed.', 'ko'];
    }
  }

  // Approvals.
  document.querySelectorAll('[data-approve]').forEach((btn) => btn.addEventListener('click', async () => {
    const card = btn.closest('[data-request]');
    const who = card.querySelector('.mono').textContent;
    const { ok } = await confirm({ title: `Approve the request of ${who}?`, text: 'The license is assigned right away (or the user is queued if the pool is exhausted).', confirmLabel: 'Approve' });
    if (!ok) return;
    await busy(btn, async () => {
      try {
        const res = await api('POST', `/api/admin/requests/${btn.dataset.approve}/approve`, undefined, { timeoutMs: 45000 });
        const [msg, kind] = outcomeMessage(res, who);
        toast(msg, kind);
        card.remove();
      } catch (err) { toast(err.message, 'ko'); }
    });
  }));

  document.querySelectorAll('[data-reject]').forEach((btn) => btn.addEventListener('click', async () => {
    const card = btn.closest('[data-request]');
    const who = card.querySelector('.mono').textContent;
    const { ok, value } = await confirm({ title: `Reject the request of ${who}?`, text: 'The reason is shown to the user and kept in their history.', confirmLabel: 'Reject', input: true, inputLabel: 'Rejection reason', danger: true });
    if (!ok) return;
    try {
      await api('POST', `/api/admin/requests/${btn.dataset.reject}/reject`, { reason: value });
      toast(`Request of ${who} rejected.`, 'ok');
      card.remove();
    } catch (err) { toast(err.message, 'ko'); }
  }));

  // Waiting list.
  document.querySelectorAll('[data-cancel-queue]').forEach((btn) => btn.addEventListener('click', async () => {
    const { ok } = await confirm({ title: 'Remove this user from the waiting list?', confirmLabel: 'Remove', danger: true });
    if (!ok) return;
    try { await api('POST', `/api/admin/queue/${btn.dataset.cancelQueue}/cancel`); location.reload(); } catch (err) { toast(err.message, 'ko'); }
  }));

  // Buffer.
  document.querySelectorAll('[data-retry-buffer]').forEach((btn) => btn.addEventListener('click', () => busy(btn, async () => {
    try {
      const res = await api('POST', `/api/admin/buffer/${btn.dataset.retryBuffer}/retry`, undefined, { timeoutMs: 60000 });
      toast(`Retry result: ${res.outcome}`, res.outcome === 'ASSIGNED' ? 'ok' : 'warn');
      setTimeout(() => location.reload(), 1200);
    } catch (err) { toast(err.message, 'ko'); }
  })));

  // GPS codes allowlist.
  const gps = document.getElementById('gps-form');
  gps.addEventListener('submit', (e) => {
    e.preventDefault();
    busy(gps.querySelector('button[type="submit"]'), async () => {
      try {
        const res = await api('POST', '/api/admin/gps', { codes: gps.querySelector('#gps-codes').value, description: gps.querySelector('#gps-description').value });
        toast(`${res.added} code(s) added.`, 'ok');
        setTimeout(() => location.reload(), 900);
      } catch (err) { toast(err.message, 'ko'); }
    });
  });
  document.querySelectorAll('[data-remove-gps]').forEach((btn) => btn.addEventListener('click', async () => {
    const code = btn.dataset.removeGps;
    const { ok } = await confirm({ title: `Remove GPS code ${code}?`, text: 'New requests with this code will need an administrator. Existing licenses are kept and flagged by the nightly directory check.', confirmLabel: 'Remove', danger: true });
    if (!ok) return;
    try { await api('DELETE', `/api/admin/gps/${encodeURIComponent(code)}`); location.reload(); } catch (err) { toast(err.message, 'ko'); }
  }));

  // Notifications: show the text version of an e-mail.
  document.querySelectorAll('[data-view-mail]').forEach((btn) => btn.addEventListener('click', () => {
    confirm({ title: btn.dataset.subject, text: btn.dataset.body, confirmLabel: 'Close', pre: true });
  }));

  // Assign.
  const assign = document.getElementById('assign-form');
  assign.addEventListener('submit', (e) => {
    e.preventDefault();
    const email = assign.querySelector('#assign-email').value.trim();
    busy(assign.querySelector('button[type="submit"]'), async () => {
      try {
        const res = await api('POST', '/api/admin/assign', { email, duration: assign.querySelector('#assign-duration').value }, { timeoutMs: 45000 });
        const [msg, kind] = outcomeMessage(res, email);
        toast(msg, kind);
        assign.reset();
      } catch (err) { toast(err.message, 'ko'); }
    });
  });

  // Transfer.
  const transfer = document.getElementById('transfer-form');
  transfer.addEventListener('submit', async (e) => {
    e.preventDefault();
    const from = transfer.querySelector('#transfer-from').value.trim();
    const to = transfer.querySelector('#transfer-to').value.trim();
    const { ok } = await confirm({ title: 'Transfer the license?', text: `${from} loses the license (end reason: admin removal), ${to} receives it.`, confirmLabel: 'Transfer', danger: true });
    if (!ok) return;
    busy(transfer.querySelector('button[type="submit"]'), async () => {
      try {
        const res = await api('POST', '/api/admin/transfer', { from, to, duration: transfer.querySelector('#transfer-duration').value }, { timeoutMs: 60000 });
        const [msg, kind] = outcomeMessage(res, to);
        toast(msg, kind);
        transfer.reset();
      } catch (err) { toast(err.message, 'ko'); }
    });
  });
})();
