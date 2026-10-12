/* =============================================================================
   Observability page: charts (Chart.js), deviations, inventory, jobs.
   Colours come from the design tokens (DESIGN.md § Data visualisation).
   ============================================================================= */
'use strict';

(() => {
  const { api, el, toast, confirm, busy, date, relative } = GCA;
  const page = GCA.pageData();

  // ---------------------------------------------------------------------------
  // Chart theme
  // ---------------------------------------------------------------------------
  const C = {
    text: '#a6adc8', grid: 'rgba(205, 214, 244, 0.06)',
    green: '#a6e3a1', teal: '#94e2d5', lavender: '#b4befe', blue: '#89b4fa', sapphire: '#74c7ec',
    peach: '#fab387', yellow: '#f9e2af', red: '#e78284', maroon: '#c98a7d', mauve: '#cba6f7',
    overlay: '#7f849c', flamingo: '#d9c4b8',
    // UI accent gradient (faded teal → green → lime), same stops as app.css.
    acc: ['#4fa596', '#7dbb86', '#bfd38a'], neutral: '#9399b2',
  };
  Chart.defaults.color = C.text;
  Chart.defaults.font.family = 'Roboto, system-ui, sans-serif';
  Chart.defaults.font.size = 12;
  Chart.defaults.borderColor = C.grid;
  Chart.defaults.plugins.legend.labels.boxWidth = 10;
  Chart.defaults.plugins.legend.labels.boxHeight = 10;
  Chart.defaults.plugins.legend.labels.usePointStyle = true;
  Chart.defaults.plugins.tooltip.backgroundColor = '#1e1e2e';
  Chart.defaults.plugins.tooltip.borderColor = 'rgba(205,214,244,.16)';
  Chart.defaults.plugins.tooltip.borderWidth = 1;
  Chart.defaults.maintainAspectRatio = false;

  const charts = {};
  /** Creates or replaces a chart on a canvas. */
  function draw(id, config) {
    if (charts[id]) charts[id].destroy();
    charts[id] = new Chart(document.getElementById(id), config);
  }
  const dayLabel = (iso) => new Date(iso).toLocaleDateString('en-GB', { day: '2-digit', month: 'short' });
  const stackedScales = (extra = {}) => ({
    x: { stacked: true, grid: { display: false } },
    y: { stacked: true, beginAtZero: true, ticks: { precision: 0 }, ...extra },
  });

  // ---------------------------------------------------------------------------
  // License flows (daily diverging bars + period waterfall)
  // ---------------------------------------------------------------------------
  async function loadFlows(days) {
    const { points } = await api('GET', `/api/obs/flows?days=${days}`);
    const labels = points.map((p) => dayLabel(p.day));
    const neg = (k) => points.map((p) => -p[k]);
    draw('chart-flows', {
      data: {
        labels,
        datasets: [
          { type: 'bar', label: 'Auto assignment', data: points.map((p) => p.assignAuto), backgroundColor: C.green, stack: 'f' },
          { type: 'bar', label: 'Admin assignment', data: points.map((p) => p.assignAdmin), backgroundColor: C.teal, stack: 'f' },
          { type: 'bar', label: 'Other assignment', data: points.map((p) => p.assignOther), backgroundColor: C.lavender, stack: 'f' },
          { type: 'bar', label: 'Removed: unused', data: neg('unassignUnused'), backgroundColor: C.peach, stack: 'f' },
          { type: 'bar', label: 'Removed: expired', data: neg('unassignExpired'), backgroundColor: C.yellow, stack: 'f' },
          { type: 'bar', label: 'Removed: admin', data: neg('unassignAdmin'), backgroundColor: C.red, stack: 'f' },
          { type: 'bar', label: 'Removed: other', data: neg('unassignOther'), backgroundColor: C.maroon, stack: 'f' },
          { type: 'line', label: 'Free seats (end of day)', data: points.map((p) => Math.max(0, (p.seatsTotal ?? 0) - p.assignedEnd)),
            borderColor: C.acc[2], backgroundColor: C.acc[2], borderDash: [5, 4], pointRadius: 0, tension: 0.3, stack: 'line' },
        ],
      },
      options: {
        interaction: { mode: 'index', intersect: false },
        scales: { x: { stacked: true, grid: { display: false } }, y: { stacked: true, ticks: { precision: 0 } } },
        plugins: { tooltip: { callbacks: { label: (c) => `${c.dataset.label}: ${Math.abs(c.parsed.y)}` } } },
      },
    });

    // Period waterfall: start → + → − → end, plus the free seat gap.
    const sum = (k) => points.reduce((a, p) => a + p[k], 0);
    const first = points[0], last = points[points.length - 1];
    const net0 = first.assignAuto + first.assignAdmin + first.assignOther - first.unassignUnused - first.unassignExpired - first.unassignAdmin - first.unassignOther;
    const start = first.assignedEnd - net0;
    const steps = [
      ['+ Auto', sum('assignAuto'), C.green], ['+ Admin', sum('assignAdmin'), C.teal], ['+ Other', sum('assignOther'), C.lavender],
      ['− Unused', -sum('unassignUnused'), C.peach], ['− Expired', -sum('unassignExpired'), C.yellow],
      ['− Admin', -sum('unassignAdmin'), C.red], ['− Other', -sum('unassignOther'), C.maroon],
    ];
    const wfLabels = ['Start'], wfData = [[0, start]], wfColors = [C.neutral];
    let level = start;
    for (const [label, delta, color] of steps) {
      wfLabels.push(label);
      wfData.push([level, level + delta]);
      wfColors.push(color);
      level += delta;
    }
    wfLabels.push('End'); wfData.push([0, last.assignedEnd]); wfColors.push(C.neutral);
    const seats = last.seatsTotal ?? last.assignedEnd;
    wfLabels.push('Free seats'); wfData.push([last.assignedEnd, Math.max(seats, last.assignedEnd)]); wfColors.push('rgba(191,211,138,0.22)');
    draw('chart-waterfall', {
      type: 'bar',
      data: { labels: wfLabels, datasets: [{ label: 'Seats', data: wfData, backgroundColor: wfColors, borderRadius: 6, borderSkipped: false }] },
      options: {
        plugins: { legend: { display: false }, tooltip: { callbacks: { label: (c) => { const [a, b] = c.raw; return `${c.label}: ${c.dataIndex === 0 || c.label === 'End' ? b : (b - a > 0 ? '+' : '') + (b - a)}`; } } } },
        scales: { x: { grid: { display: false } }, y: { beginAtZero: true, ticks: { precision: 0 } } },
      },
    });
  }

  // ---------------------------------------------------------------------------
  // Queue / buffer / deviation depths
  // ---------------------------------------------------------------------------
  async function loadDepths(days) {
    const pts = await api('GET', `/api/obs/depths?days=${days}`);
    const labels = pts.map((p) => dayLabel(p.day));
    const area = (id, label, key, color) => draw(id, {
      type: 'line',
      data: { labels, datasets: [{ label, data: pts.map((p) => p[key]), borderColor: color, backgroundColor: `${color}33`, fill: true, tension: 0.35, pointRadius: 0, pointHoverRadius: 4 }] },
      options: { plugins: { legend: { display: false } }, scales: { x: { grid: { display: false } }, y: { beginAtZero: true, ticks: { precision: 0 } } } },
    });
    area('chart-queue', 'Waiting list', 'queue', C.blue);
    area('chart-buffer', 'Buffer', 'buffer', C.peach);
    draw('chart-deviations', {
      type: 'bar',
      data: { labels, datasets: [{ label: 'Open deviations', data: pts.map((p) => p.deviations), backgroundColor: C.red, borderRadius: 4 }] },
      options: { plugins: { legend: { display: false } }, scales: { x: { grid: { display: false } }, y: { beginAtZero: true, ticks: { precision: 0 } } } },
    });
  }

  // ---------------------------------------------------------------------------
  // Technical KPIs
  // ---------------------------------------------------------------------------
  async function loadTech() {
    const k = await api('GET', '/api/obs/tech');
    const labels = k.days.map(dayLabel);
    const byDay = (rows, pred, val) => k.days.map((d) => rows.filter((r) => r.day.slice(0, 10) === d && pred(r)).reduce((a, r) => a + val(r), 0));

    // Directory errors, stacked by type.
    const types = [['TIMEOUT', C.peach], ['HTTP_5XX', C.red], ['HTTP_4XX', C.maroon], ['NETWORK', C.yellow], ['RATE_LIMITED', C.mauve], ['ERROR', C.flamingo], ['NOT_FOUND', C.overlay]];
    draw('chart-ed-errors', {
      type: 'bar',
      data: { labels, datasets: types.map(([t, color]) => ({ label: t === 'NOT_FOUND' ? 'NOT_FOUND (unknown account)' : t, data: byDay(k.directory || [], (r) => r.outcome === t, (r) => r.n), backgroundColor: color, stack: 'e' })) },
      options: { scales: stackedScales() },
    });

    // Latency charts.
    const latency = (id, target, op) => {
      const rows = (k.latencies || []).filter((r) => r.target === target && r.operation === op);
      const pick = (key) => k.days.map((d) => { const r = rows.find((x) => x.day.slice(0, 10) === d); return r ? Math.round(r[key]) : null; });
      draw(id, {
        type: 'line',
        data: { labels, datasets: [
          { label: 'p50', data: pick('p50'), borderColor: C.acc[0], backgroundColor: C.acc[0], tension: 0.3, spanGaps: true, pointRadius: 2 },
          { label: 'p95', data: pick('p95'), borderColor: C.acc[2], backgroundColor: C.acc[2], tension: 0.3, spanGaps: true, pointRadius: 2, borderDash: [4, 3] },
        ] },
        options: { scales: { x: { grid: { display: false } }, y: { beginAtZero: true, title: { display: true, text: 'ms' } } } },
      });
    };
    latency('chart-ed-latency', 'DIRECTORY', 'lookup');
    latency('chart-cred-latency', 'GOOGLE_CREDENTIALS', 'grant');
    latency('chart-assign-latency', 'GOOGLE_LICENSE', 'assign');

    // Timeouts: initial process (UX) vs retry mechanism (JOB) vs admin actions.
    draw('chart-timeouts', {
      type: 'bar',
      data: { labels, datasets: [
        { label: 'Initial process (user waiting)', data: byDay(k.timeouts || [], (r) => r.context === 'UX', (r) => r.n), backgroundColor: C.red, stack: 't' },
        { label: 'Retry mechanism (jobs)', data: byDay(k.timeouts || [], (r) => r.context === 'JOB', (r) => r.n), backgroundColor: C.peach, stack: 't' },
        { label: 'Admin actions', data: byDay(k.timeouts || [], (r) => r.context === 'ADMIN', (r) => r.n), backgroundColor: C.mauve, stack: 't' },
      ] },
      options: { scales: stackedScales() },
    });

    // App access vs Google license checks.
    draw('chart-access', {
      type: 'line',
      data: { labels: (k.accesses || []).map((a) => dayLabel(a.day)), datasets: [
        { label: 'Page views', data: (k.accesses || []).map((a) => a.pageViews), borderColor: C.acc[2], backgroundColor: `${C.acc[2]}1f`, fill: true, tension: 0.3, pointRadius: 0 },
        { label: 'Google license checks', data: (k.accesses || []).map((a) => a.googleChecks), borderColor: C.acc[0], backgroundColor: C.acc[0], tension: 0.3, pointRadius: 0, borderDash: [5, 4] },
      ] },
      options: { interaction: { mode: 'index', intersect: false }, scales: { x: { grid: { display: false } }, y: { beginAtZero: true } } },
    });
  }

  // ---------------------------------------------------------------------------
  // Deviations
  // ---------------------------------------------------------------------------
  async function loadDeviations() {
    const rows = await api('GET', '/api/obs/deviations');
    const body = document.getElementById('deviations-body');
    if (!rows.length) {
      body.replaceChildren(el('tr', {}, el('td', { colspan: 4, class: 'table-empty' }, 'No deviation. Portal and Google agree.')));
      return;
    }
    body.replaceChildren(...rows.map((d) => {
      const actions = d.kind === 'GOOGLE_ONLY'
        ? [['remove', 'Remove from Google', 'btn--danger'], ['backport', 'Back-port', 'btn--secondary']]
        : [['close', 'Close record', 'btn--secondary'], ['reassign', 'Re-assign', 'btn--secondary']];
      return el('tr', {},
        el('td', {}, d.userEmail),
        el('td', {}, el('span', { class: `badge ${d.kind === 'GOOGLE_ONLY' ? 'badge--warn' : 'badge--ko'}` }, d.kind)),
        el('td', {}, date(d.detectedAt), el('span', { class: 'sub' }, relative(d.detectedAt))),
        el('td', {}, el('div', { class: 'row' }, actions.map(([action, label, cls]) =>
          el('button', { class: `btn btn--sm ${cls}`, type: 'button', onclick: (e) => resolveDeviation(e.currentTarget, d, action, label) }, label)))));
    }));
  }

  async function resolveDeviation(btn, d, action, label) {
    const { ok } = await confirm({ title: `${label} — ${d.userEmail}?`, text: 'Google stays the source of truth; this action is audited.', confirmLabel: label, danger: action === 'remove' });
    if (!ok) return;
    await busy(btn, async () => {
      try {
        await api('POST', `/api/obs/deviations/${d.id}/resolve`, { action }, { timeoutMs: 45000 });
        toast('Deviation resolved.', 'ok');
        loadDeviations();
        loadDepths(currentDays);
      } catch (err) { toast(err.message, 'ko'); }
    });
  }

  // ---------------------------------------------------------------------------
  // Inventory: filter chips, selection, removal
  // ---------------------------------------------------------------------------
  const inv = { filter: { search: '', sites: [], businessUnits: [], sources: [], unusedDaysMin: 0, expiresInDays: 0, accountType: '', directoryCheck: [] }, rows: [], total: 0, selected: new Set(), facetsBuilt: false };

  const DIR_LABEL = { CHANGED: 'Data changed', INELIGIBLE: 'No longer eligible', ERROR: 'Check failed', UNCHECKED: 'Never checked' };

  /** Badge of the monthly directory re-check of a license. */
  function dirBadge(r) {
    if (r.ineligibleReason) return el('span', { class: 'badge badge--ko', title: r.ineligibleReason }, 'not eligible');
    if ((r.driftFields || []).length) {
      return el('span', { class: 'badge badge--warn', title: `Changed since assignment: ${r.driftFields.join(', ')} (now ${r.dirCurrentSite || '—'} / ${r.dirCurrentBusinessUnit || '—'} / ${r.dirCurrentGpsCode || '—'})` }, 'changed');
    }
    if (r.dirCheckResult === 'ERROR') return el('span', { class: 'badge badge--warn', title: r.dirCheckError || '' }, 'check failed');
    if (!r.dirCheckedAt) return el('span', { class: 'badge badge--plain' }, 'not checked');
    return el('span', { class: 'badge badge--ok', title: `Checked ${date(r.dirCheckedAt)}` }, 'ok');
  }

  function chipGroup(label, values, key, { single = false, render = (v) => v } = {}) {
    const group = el('div', { class: 'filter-group', role: 'group', 'aria-label': label }, el('span', { class: 'filter-group__label' }, label));
    values.forEach((v) => {
      const chip = el('button', { class: 'chip chip--sm', type: 'button', 'aria-pressed': 'false' }, render(v));
      chip.addEventListener('click', () => {
        const on = chip.getAttribute('aria-pressed') !== 'true';
        if (single) {
          group.querySelectorAll('.chip').forEach((c) => c.setAttribute('aria-pressed', 'false'));
          inv.filter[key] = on ? v : (typeof v === 'string' ? '' : 0);
        } else {
          const set = new Set(inv.filter[key]);
          on ? set.add(v) : set.delete(v);
          inv.filter[key] = [...set];
        }
        chip.setAttribute('aria-pressed', String(on));
        loadInventory();
      });
      group.append(chip);
    });
    return group;
  }

  function buildFacets(f) {
    const box = document.getElementById('inv-filters');
    box.replaceChildren(
      chipGroup('Site', f.sites || [], 'sites'),
      chipGroup('Business unit', f.businessUnits || [], 'businessUnits'),
      chipGroup('Account', ['internal', 'external'], 'accountType', { single: true, render: (v) => (v === 'external' ? `External (*${page.externalSuffix}@)` : 'Internal') }),
      chipGroup('Source', f.sources || [], 'sources', { render: (v) => v.toLowerCase() }),
      chipGroup('Directory check', ['CHANGED', 'INELIGIBLE', 'ERROR', 'UNCHECKED'], 'directoryCheck', { render: (v) => DIR_LABEL[v] }),
      chipGroup('Unused for', [page.unusedDays || 7, 14, 30], 'unusedDaysMin', { single: true, render: (v) => `≥ ${v} days` }),
      chipGroup('Expires within', [7, 30], 'expiresInDays', { single: true, render: (v) => `${v} days` }),
    );
    inv.facetsBuilt = true;
  }

  async function loadInventory() {
    let v;
    try { v = await api('POST', '/api/admin/inventory', inv.filter); } catch (err) { toast(err.message, 'ko'); return; }
    if (!inv.facetsBuilt) buildFacets(v.facets);
    inv.rows = v.rows; inv.total = v.total;
    inv.selected = new Set([...inv.selected].filter((e) => v.rows.some((r) => r.userEmail === e)));
    const body = document.getElementById('inv-body');
    document.getElementById('inv-count').textContent = `${v.total} license${v.total > 1 ? 's' : ''} match${v.total > 1 ? '' : 'es'}`;
    if (!v.rows.length) {
      body.replaceChildren(el('tr', {}, el('td', { colspan: 10, class: 'table-empty' }, 'No license matches these filters.')));
    } else {
      body.replaceChildren(...v.rows.map((r) => {
        const cb = el('input', { type: 'checkbox', 'aria-label': `Select ${r.userEmail}` });
        cb.checked = inv.selected.has(r.userEmail);
        cb.addEventListener('change', () => { cb.checked ? inv.selected.add(r.userEmail) : inv.selected.delete(r.userEmail); refreshSelection(); });
        const usage = r.googleLastUsage || r.lastUsageAt;
        return el('tr', {},
          el('td', {}, cb),
          el('td', {}, r.userEmail, r.external ? el('span', { class: 'sub' }, el('span', { class: 'badge badge--accent' }, 'external')) : null),
          el('td', {}, r.site || '—'),
          el('td', {}, r.businessUnit || '—'),
          el('td', {}, el('span', { class: `badge ${r.source === 'AUTO' ? 'badge--ok' : r.source === 'ADMIN' ? 'badge--info' : 'badge--accent'}` }, r.source.toLowerCase())),
          el('td', {}, date(r.assignedAt)),
          el('td', {}, date(r.expiresAt), el('span', { class: 'sub' }, relative(r.expiresAt))),
          el('td', {}, usage ? date(usage) : 'Never', el('span', { class: `sub ${r.unusedDays >= (page.unusedDays || 7) - 2 ? 'text-warn' : ''}` }, `${r.unusedDays} day(s) without usage`)),
          el('td', {}, dirBadge(r), r.dirCheckedAt ? el('span', { class: 'sub' }, date(r.dirCheckedAt)) : null),
          el('td', {}, r.inGoogle ? el('span', { class: 'badge badge--ok' }, 'yes') : el('span', { class: 'badge badge--ko' }, 'missing')));
      }));
    }
    refreshSelection();
  }

  function refreshSelection() {
    const n = inv.selected.size;
    document.getElementById('inv-selected').textContent = `${n} selected · ${inv.total} matching the filters`;
    document.getElementById('inv-remove-selected').disabled = n === 0;
    document.getElementById('inv-recheck').disabled = n === 0 || n > 50;
    document.getElementById('inv-ack').disabled = n === 0;
    const matching = document.getElementById('inv-remove-matching');
    matching.disabled = inv.total === 0;
    matching.textContent = `Remove all matching (${inv.total})`;
    const all = document.getElementById('inv-all');
    all.checked = n > 0 && n === inv.rows.length;
    all.indeterminate = n > 0 && n < inv.rows.length;
  }

  document.getElementById('inv-all').addEventListener('change', (e) => {
    inv.selected = e.target.checked ? new Set(inv.rows.map((r) => r.userEmail)) : new Set();
    loadInventory();
  });

  let searchTimer;
  document.getElementById('inv-search').addEventListener('input', (e) => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => { inv.filter.search = e.target.value; loadInventory(); }, 250);
  });

  function removalResult(res) {
    const failed = Object.keys(res.failed || {});
    toast(`${(res.removed || []).length} license(s) removed${failed.length ? `, ${failed.length} failed` : ''}. Released seats go to the waiting list.`, failed.length ? 'warn' : 'ok', 7000);
    inv.selected.clear();
    loadInventory();
  }

  document.getElementById('inv-remove-selected').addEventListener('click', async (e) => {
    const emails = [...inv.selected];
    const { ok } = await confirm({ title: `Remove ${emails.length} license(s)?`, text: 'End reason: admin removal. Users are notified by e-mail.', confirmLabel: 'Remove', danger: true });
    if (!ok) return;
    await busy(e.currentTarget, async () => {
      try { removalResult(await api('POST', '/api/admin/unassign', { emails }, { timeoutMs: 120000 })); } catch (err) { toast(err.message, 'ko'); }
    });
  });

  document.getElementById('inv-recheck').addEventListener('click', (e) => busy(e.currentTarget, async () => {
    try {
      const res = await api('POST', '/api/admin/directory/recheck', { emails: [...inv.selected] }, { timeoutMs: 120000 });
      const counts = Object.values(res.results).reduce((a, v) => { a[v] = (a[v] || 0) + 1; return a; }, {});
      toast(`Directory re-check: ${Object.entries(counts).map(([k, v]) => `${v} ${k.toLowerCase()}`).join(', ')}`, 'ok', 7000);
      loadInventory();
    } catch (err) { toast(err.message, 'ko'); }
  }));

  document.getElementById('inv-ack').addEventListener('click', async (e) => {
    const { ok } = await confirm({ title: `Accept the directory changes of ${inv.selected.size} license(s)?`, text: 'The current site, business unit and GPS code become the reference. "No longer eligible" flags are kept until the cause is fixed.', confirmLabel: 'Accept' });
    if (!ok) return;
    await busy(e.currentTarget, async () => {
      try {
        const res = await api('POST', '/api/admin/directory/acknowledge', { emails: [...inv.selected] });
        toast(`${res.acknowledged} license(s) updated.`, 'ok');
        loadInventory();
      } catch (err) { toast(err.message, 'ko'); }
    });
  });

  document.getElementById('inv-remove-matching').addEventListener('click', async (e) => {
    const { ok } = await confirm({ title: `Mass removal: ${inv.total} license(s)`, text: 'Every license matching the current filters is removed (end reason: admin removal). If the selection changes before confirmation, the operation is refused.', confirmLabel: `Remove ${inv.total}`, danger: true });
    if (!ok) return;
    await busy(e.currentTarget, async () => {
      try { removalResult(await api('POST', '/api/admin/unassign-by-filter', { filter: inv.filter, expected: inv.total }, { timeoutMs: 180000 })); } catch (err) { toast(err.message, 'ko'); }
    });
  });

  // ---------------------------------------------------------------------------
  // Super admin: jobs, refresh, simulation
  // ---------------------------------------------------------------------------
  async function runJob(btn, url, label) {
    await busy(btn, async () => {
      try {
        const res = await api('POST', url, undefined, { timeoutMs: 300000 });
        toast(`${label}: ${res.status} — ${res.summary || res.error || ''}`, res.status === 'OK' ? 'ok' : 'warn', 8000);
        setTimeout(() => location.reload(), 1500);
      } catch (err) { toast(err.message, 'ko'); }
    });
  }
  const refresh = document.getElementById('force-refresh');
  if (refresh) refresh.addEventListener('click', () => runJob(refresh, '/api/super/refresh', 'Subscription refresh'));
  document.querySelectorAll('[data-run-job]').forEach((b) => b.addEventListener('click', () => runJob(b, `/api/super/jobs/${b.dataset.runJob}`, b.dataset.runJob)));

  const simCall = async (btn, url, body, label) => busy(btn, async () => {
    try { const r = await api('POST', url, body, { timeoutMs: 60000 }); toast(`${label}: ${r.summary || 'done'}`, 'ok', 8000); setTimeout(() => location.reload(), 1500); } catch (err) { toast(err.message, 'ko'); }
  });
  const seatsBtn = document.getElementById('sim-seats-btn');
  if (seatsBtn) {
    seatsBtn.addEventListener('click', () => simCall(seatsBtn, '/api/super/sim/seats', { seats: Number(document.getElementById('sim-seats').value) }, 'Seats changed'));
    const roll = document.getElementById('sim-rollover-btn');
    roll.addEventListener('click', async () => {
      const { ok } = await confirm({ title: 'Simulate a subscription renewal?', text: 'The current fake subscription ends now and a new one starts with the pool size above. Active licenses are migrated.', confirmLabel: 'Simulate' });
      if (ok) simCall(roll, '/api/super/sim/rollover', { seats: Number(document.getElementById('sim-seats').value) }, 'Rollover');
    });
    const rm = document.getElementById('sim-remove-btn');
    rm.addEventListener('click', () => simCall(rm, '/api/super/sim/remove', { email: document.getElementById('sim-remove').value }, 'Removed in Google'));
  }

  // ---------------------------------------------------------------------------
  // Period selector and initial load
  // ---------------------------------------------------------------------------
  let currentDays = 30;
  GCA.radioChips(document.getElementById('period'), (chip) => {
    currentDays = Number(chip.dataset.days);
    loadFlows(currentDays).catch((e) => toast(e.message, 'ko'));
    loadDepths(currentDays).catch((e) => toast(e.message, 'ko'));
  });

  Promise.all([loadFlows(currentDays), loadDepths(currentDays), loadTech(), loadDeviations(), loadInventory()])
    .catch((e) => toast(`Some data could not be loaded: ${e.message}`, 'ko'));
})();
