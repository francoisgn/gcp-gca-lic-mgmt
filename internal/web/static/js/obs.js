/* =============================================================================
   Observability page: charts (Chart.js), deviations, inventory, jobs.
   Colours come from the design tokens (DESIGN.md § Data visualisation).
   ============================================================================= */
'use strict';

(() => {
  const { api, el, toast, confirm, busy, date, relative } = GCA;
  const page = GCA.pageData();

  // ---------------------------------------------------------------------------
  // Chart theme — "dashboard gradients" (DESIGN.md § Data visualisation):
  // multi-hue gradients, thin smooth lines, filled areas faded to transparent,
  // rounded bars. Saturated hues, but never as large solid surfaces.
  // ---------------------------------------------------------------------------
  const G = {
    violet: ['#7b6cf6', '#d77fc0'], // violet → pink (signature line)
    blue: ['#5f73f2', '#46b3d6'],   // indigo → cyan
    amber: ['#f2c66d', '#ec8f5e'],  // amber → orange
    yellow: ['#f5dc8f', '#f2c66d'],
    green: ['#4fa596', '#bfd38a'],  // UI accent: teal → lime
    rose: ['#d77fc0', '#e78284'],   // pink → soft red
    lilac: ['#a6a1f0', '#b4befe'],
    grey: ['#7f849c', '#585b70'],
  };
  Chart.defaults.color = '#a6adc8';
  Chart.defaults.font.family = 'Roboto, system-ui, sans-serif';
  Chart.defaults.font.size = 12;
  Chart.defaults.borderColor = 'rgba(205, 214, 244, 0.05)';
  Chart.defaults.maintainAspectRatio = false;
  // Monotone curves: smooth like the reference dashboard, without overshooting the data.
  Object.assign(Chart.defaults.elements.line, { borderWidth: 2, tension: 0.4, cubicInterpolationMode: 'monotone', borderCapStyle: 'round' });
  Object.assign(Chart.defaults.elements.point, { radius: 0, hoverRadius: 4, hitRadius: 10, hoverBorderWidth: 2 });
  Object.assign(Chart.defaults.elements.bar, { borderRadius: 4, borderSkipped: false });
  Chart.defaults.datasets.bar.maxBarThickness = 16;
  Object.assign(Chart.defaults.plugins.legend.labels, { usePointStyle: true, pointStyle: 'circle', boxWidth: 7, boxHeight: 7, padding: 14 });
  Object.assign(Chart.defaults.plugins.tooltip, {
    backgroundColor: 'rgba(30, 30, 46, 0.95)', borderColor: 'rgba(205,214,244,.12)', borderWidth: 1,
    padding: 10, cornerRadius: 10, usePointStyle: true, boxPadding: 4,
  });
  // Legend dots use the solid colour of the dataset (gradients would be clipped).
  const baseLabels = Chart.defaults.plugins.legend.labels.generateLabels;
  Chart.defaults.plugins.legend.labels.generateLabels = (chart) => baseLabels(chart).map((item) => {
    const ds = chart.data.datasets[item.datasetIndex];
    if (ds && ds.legendColor) Object.assign(item, { fillStyle: ds.legendColor, strokeStyle: ds.legendColor, lineWidth: 0 });
    return item;
  });

  /** rgba() string from a #rrggbb colour. */
  const rgba = (hex, a) => { const n = parseInt(hex.slice(1), 16); return `rgba(${n >> 16}, ${(n >> 8) & 255}, ${n & 255}, ${a})`; };
  /** Middle colour of a gradient (legend dots, points). */
  const mid = ([a, b]) => {
    const x = parseInt(a.slice(1), 16), y = parseInt(b.slice(1), 16);
    const m = (s) => Math.round((((x >> s) & 255) + ((y >> s) & 255)) / 2);
    return `#${[16, 8, 0].map((s) => m(s).toString(16).padStart(2, '0')).join('')}`;
  };
  /** Scriptable gradient across the chart area, left → right (lines, bars over time). */
  const across = ([a, b], alpha = 1) => ({ chart }) => {
    const { chartArea: r, ctx } = chart;
    if (!r) return rgba(a, alpha);
    const g = ctx.createLinearGradient(r.left, 0, r.right, 0);
    g.addColorStop(0, rgba(a, alpha)); g.addColorStop(1, rgba(b, alpha));
    return g;
  };
  /** Scriptable gradient top → bottom (categorical bars). */
  const down = ([a, b], alpha = 0.9) => ({ chart }) => {
    const { chartArea: r, ctx } = chart;
    if (!r) return rgba(a, alpha);
    const g = ctx.createLinearGradient(0, r.top, 0, r.bottom);
    g.addColorStop(0, rgba(a, alpha)); g.addColorStop(1, rgba(b, alpha * 0.75));
    return g;
  };
  /** Scriptable fill for areas: the colour fades to transparent towards the axis. */
  const fade = (hex, alpha = 0.34) => ({ chart }) => {
    const { chartArea: r, ctx } = chart;
    if (!r) return rgba(hex, alpha / 2);
    const g = ctx.createLinearGradient(0, r.top, 0, r.bottom);
    g.addColorStop(0, rgba(hex, alpha)); g.addColorStop(0.7, rgba(hex, alpha / 5)); g.addColorStop(1, rgba(hex, 0));
    return g;
  };
  /** Line dataset: thin gradient stroke, optionally a faded filled area. */
  const line = (label, data, grad, { fill = false, dash, width } = {}) => ({
    type: 'line', label, data, fill, borderDash: dash, borderWidth: width, spanGaps: true,
    borderColor: across(grad), backgroundColor: fill ? fade(mid(grad)) : mid(grad),
    pointBackgroundColor: mid(grad), pointBorderColor: '#11111b', legendColor: mid(grad),
  });
  /** Bar dataset: gradient across the period, slightly transparent. */
  const bar = (label, data, grad, stack) => ({
    type: 'bar', label, data, stack,
    backgroundColor: across(grad, 0.8), hoverBackgroundColor: across(grad, 1), legendColor: mid(grad),
  });

  const charts = {};
  /** Creates or replaces a chart on a canvas. */
  function draw(id, config) {
    if (charts[id]) charts[id].destroy();
    charts[id] = new Chart(document.getElementById(id), config);
  }
  const dayLabel = (iso) => new Date(iso).toLocaleDateString('en-GB', { day: '2-digit', month: 'short' });
  const xAxis = { grid: { display: false }, border: { display: false }, ticks: { maxRotation: 0, autoSkipPadding: 12 } };
  const yAxis = (extra = {}) => ({ beginAtZero: true, border: { display: false }, ticks: { precision: 0, padding: 8 }, ...extra });
  const stackedScales = () => ({ x: { ...xAxis, stacked: true }, y: yAxis({ stacked: true }) });

  // ---------------------------------------------------------------------------
  // License flows (daily diverging bars + period waterfall)
  // ---------------------------------------------------------------------------
  async function loadFlows(days) {
    const { points } = await api('GET', `/api/obs/flows?days=${days}`);
    const labels = points.map((p) => dayLabel(p.day));
    const pos = (k) => points.map((p) => p[k]);
    const neg = (k) => points.map((p) => -p[k]);
    draw('chart-flows', {
      data: {
        labels,
        datasets: [
          bar('Auto assignment', pos('assignAuto'), G.green, 'f'),
          bar('Admin assignment', pos('assignAdmin'), G.blue, 'f'),
          bar('Other assignment', pos('assignOther'), G.lilac, 'f'),
          bar('Removed: unused', neg('unassignUnused'), G.amber, 'f'),
          bar('Removed: expired', neg('unassignExpired'), G.yellow, 'f'),
          bar('Removed: admin', neg('unassignAdmin'), G.rose, 'f'),
          bar('Removed: other', neg('unassignOther'), G.grey, 'f'),
          { ...line('Free seats (end of day)', points.map((p) => Math.max(0, (p.seatsTotal ?? 0) - p.assignedEnd)), G.violet, { width: 1.5 }), stack: 'line' },
        ],
      },
      options: {
        interaction: { mode: 'index', intersect: false },
        scales: { x: { ...xAxis, stacked: true }, y: yAxis({ stacked: true, beginAtZero: false }) },
        plugins: { tooltip: { callbacks: { label: (c) => `${c.dataset.label}: ${Math.abs(c.parsed.y)}` } } },
      },
    });

    // Period waterfall: start → + → − → end, plus the free seat gap.
    const sum = (k) => points.reduce((a, p) => a + p[k], 0);
    const first = points[0], last = points[points.length - 1];
    const net0 = first.assignAuto + first.assignAdmin + first.assignOther - first.unassignUnused - first.unassignExpired - first.unassignAdmin - first.unassignOther;
    const start = first.assignedEnd - net0;
    const steps = [
      ['+ Auto', sum('assignAuto'), G.green], ['+ Admin', sum('assignAdmin'), G.blue], ['+ Other', sum('assignOther'), G.lilac],
      ['− Unused', -sum('unassignUnused'), G.amber], ['− Expired', -sum('unassignExpired'), G.yellow],
      ['− Admin', -sum('unassignAdmin'), G.rose], ['− Other', -sum('unassignOther'), G.grey],
    ];
    const wfLabels = ['Start'], wfData = [[0, start]], wfGrads = [G.violet];
    let level = start;
    for (const [label, delta, grad] of steps) {
      wfLabels.push(label);
      wfData.push([level, level + delta]);
      wfGrads.push(grad);
      level += delta;
    }
    wfLabels.push('End'); wfData.push([0, last.assignedEnd]); wfGrads.push(G.violet);
    const seats = last.seatsTotal ?? last.assignedEnd;
    wfLabels.push('Free seats'); wfData.push([last.assignedEnd, Math.max(seats, last.assignedEnd)]); wfGrads.push(null);
    draw('chart-waterfall', {
      type: 'bar',
      data: { labels: wfLabels, datasets: [{
        label: 'Seats', data: wfData, borderRadius: 6, maxBarThickness: 34,
        backgroundColor: (c) => (wfGrads[c.dataIndex] ? down(wfGrads[c.dataIndex])(c) : rgba(G.green[1], 0.16)),
        borderColor: (c) => (wfGrads[c.dataIndex] ? 'transparent' : rgba(G.green[1], 0.45)),
        borderWidth: (c) => (wfGrads[c.dataIndex] ? 0 : 1),
      }] },
      options: {
        plugins: { legend: { display: false }, tooltip: { callbacks: { label: (c) => { const [a, b] = c.raw; return `${c.label}: ${c.dataIndex === 0 || c.label === 'End' ? b : (b - a > 0 ? '+' : '') + (b - a)}`; } } } },
        scales: { x: { ...xAxis, ticks: { autoSkip: false, maxRotation: 0, font: { size: 11 } } }, y: yAxis() },
      },
    });
  }

  // ---------------------------------------------------------------------------
  // Queue / buffer / deviation depths
  // ---------------------------------------------------------------------------
  async function loadDepths(days) {
    const pts = await api('GET', `/api/obs/depths?days=${days}`);
    const labels = pts.map((p) => dayLabel(p.day));
    const area = (id, label, key, grad) => draw(id, {
      type: 'line',
      data: { labels, datasets: [line(label, pts.map((p) => p[key]), grad, { fill: true })] },
      options: { interaction: { mode: 'index', intersect: false }, plugins: { legend: { display: false } }, scales: { x: xAxis, y: yAxis() } },
    });
    area('chart-queue', 'Waiting list', 'queue', G.violet);
    area('chart-buffer', 'Buffer', 'buffer', G.amber);
    draw('chart-deviations', {
      type: 'bar',
      data: { labels, datasets: [{ ...bar('Open deviations', pts.map((p) => p.deviations), G.rose), backgroundColor: down(G.rose) }] },
      options: { plugins: { legend: { display: false } }, scales: { x: xAxis, y: yAxis() } },
    });
  }

  // ---------------------------------------------------------------------------
  // Technical KPIs
  // ---------------------------------------------------------------------------
  async function loadTech() {
    const k = await api('GET', '/api/obs/tech');
    const labels = k.days.map(dayLabel);
    const byDay = (rows, pred, val) => k.days.map((d) => rows.filter((r) => r.day.slice(0, 10) === d && pred(r)).reduce((a, r) => a + val(r), 0));

    // Directory load and availability: does the directory hold the load of the
    // user requests plus the monthly re-check? Availability = answered calls
    // (OK or "unknown account") / all calls.
    const dir = k.directory || [];
    const all = byDay(dir, () => true, (r) => r.n);
    const answered = byDay(dir, (r) => r.outcome === 'OK' || r.outcome === 'NOT_FOUND', (r) => r.n);
    draw('chart-ed-load', {
      data: { labels, datasets: [
        bar('User requests', byDay(dir, (r) => r.context === 'UX', (r) => r.n), G.blue, 'load'),
        bar('Monthly re-check', byDay(dir, (r) => r.context === 'JOB', (r) => r.n), G.lilac, 'load'),
        bar('Admin', byDay(dir, (r) => r.context === 'ADMIN', (r) => r.n), G.grey, 'load'),
        { ...line('Availability', all.map((n, i) => (n ? Math.round((1000 * answered[i]) / n) / 10 : null)), G.green, { width: 1.5 }), yAxisID: 'pct' },
      ] },
      options: {
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: { ...xAxis, stacked: true },
          y: yAxis({ stacked: true, title: { display: true, text: 'calls' } }),
          pct: { position: 'right', min: 0, max: 100, grid: { display: false }, border: { display: false }, ticks: { callback: (v) => `${v} %` } },
        },
        plugins: { tooltip: { callbacks: { label: (c) => (c.dataset.yAxisID === 'pct' ? `Availability: ${c.parsed.y} %` : `${c.dataset.label}: ${c.parsed.y}`) } } },
      },
    });

    // Directory errors, stacked by type.
    const types = [['TIMEOUT', G.amber], ['HTTP_5XX', G.rose], ['HTTP_4XX', G.violet], ['NETWORK', G.yellow], ['RATE_LIMITED', G.blue], ['ERROR', G.lilac], ['NOT_FOUND', G.grey]];
    draw('chart-ed-errors', {
      type: 'bar',
      data: { labels, datasets: types.map(([t, grad]) => bar(t === 'NOT_FOUND' ? 'NOT_FOUND (unknown account)' : t, byDay(k.directory || [], (r) => r.outcome === t, (r) => r.n), grad, 'e')) },
      options: { scales: stackedScales() },
    });

    // Latency charts: p50 as a faded area, p95 as a thin line above it.
    const latency = (id, target, op) => {
      const rows = (k.latencies || []).filter((r) => r.target === target && r.operation === op);
      const pick = (key) => k.days.map((d) => { const r = rows.find((x) => x.day.slice(0, 10) === d); return r ? Math.round(r[key]) : null; });
      draw(id, {
        type: 'line',
        data: { labels, datasets: [
          line('p50', pick('p50'), G.blue, { fill: true }),
          line('p95', pick('p95'), G.violet, { width: 1.5 }),
        ] },
        options: { interaction: { mode: 'index', intersect: false }, scales: { x: xAxis, y: yAxis({ title: { display: true, text: 'ms' } }) } },
      });
    };
    latency('chart-ed-latency', 'DIRECTORY', 'lookup');
    latency('chart-cred-latency', 'GOOGLE_CREDENTIALS', 'grant');
    latency('chart-assign-latency', 'GOOGLE_LICENSE', 'assign');

    // Timeouts: initial process (UX) vs retry mechanism (JOB) vs admin actions.
    draw('chart-timeouts', {
      type: 'bar',
      data: { labels, datasets: [
        bar('Initial process (user waiting)', byDay(k.timeouts || [], (r) => r.context === 'UX', (r) => r.n), G.rose, 't'),
        bar('Retry mechanism (jobs)', byDay(k.timeouts || [], (r) => r.context === 'JOB', (r) => r.n), G.amber, 't'),
        bar('Admin actions', byDay(k.timeouts || [], (r) => r.context === 'ADMIN', (r) => r.n), G.lilac, 't'),
      ] },
      options: { scales: stackedScales() },
    });

    // App access vs Google license checks.
    draw('chart-access', {
      type: 'line',
      data: { labels: (k.accesses || []).map((a) => dayLabel(a.day)), datasets: [
        line('Page views', (k.accesses || []).map((a) => a.pageViews), G.blue, { fill: true }),
        line('Google license checks', (k.accesses || []).map((a) => a.googleChecks), G.violet, { width: 1.5 }),
      ] },
      options: { interaction: { mode: 'index', intersect: false }, scales: { x: xAxis, y: yAxis() } },
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
