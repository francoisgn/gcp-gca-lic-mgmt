/* =============================================================================
   Landing page: progressive request form and state actions.

   Flow (each answer unlocks the next one, the left panel follows):
     A identity → B training → C duration → D usage details
     → W directory validation (OK | functional KO → W1 escalation | technical KO → buffer)
     → X credentials → Y license → Z e-mail (single call, 10 s budget, loader)
   ============================================================================= */
'use strict';

(() => {
  const { api, el, alertBox, toast, confirm, date, relative, radioChips, busy } = GCA;
  const data = GCA.pageData();
  const STEP_ORDER = ['identity', 'training', 'duration', 'details', 'directory', 'admin', 'credentials', 'license', 'email'];
  const ICON = {
    ok: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.2 4.2L19 7"/></svg>',
    ko: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 6l12 12M18 6L6 18"/></svg>',
    warn: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/></svg>',
    skipped: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 12h12"/></svg>',
  };

  // ---------------------------------------------------------------------------
  // Left panel
  // ---------------------------------------------------------------------------

  /** Updates one step of the left panel. state: todo|active|ok|warn|ko|skipped */
  function setStep(id, state, hint = '') {
    const li = document.querySelector(`#steps [data-step="${id}"]`);
    if (!li) return;
    li.className = `step step--${state}`;
    const iconBox = li.querySelector('.step__icon');
    iconBox.innerHTML = ICON[state] || String(STEP_ORDER.indexOf(id) + 1);
    li.querySelector('.step__hint').textContent = hint;
  }

  /** Applies the authoritative step list returned by the server. */
  function applySteps(steps) {
    (steps || []).forEach((s) => setStep(s.id, s.state, s.hint || ''));
  }

  // ---------------------------------------------------------------------------
  // Progressive form
  // ---------------------------------------------------------------------------
  const form = document.getElementById('request-form');
  const state = { training: false, duration: '', answers: {}, requestId: 0 };
  const prefill = data.prefill || null;

  const qa = (name) => form && form.querySelector(`[data-qa="${name}"]`);

  /** Shows a section with an entrance animation and scrolls it into view. */
  function reveal(name) {
    const node = qa(name);
    if (!node || !node.hidden) return;
    node.hidden = false;
    requestAnimationFrame(() => node.scrollIntoView({ behavior: 'smooth', block: 'nearest' }));
  }

  function hideFrom(name) {
    const order = ['training', 'duration', 'details', 'directory', 'escalation', 'submit'];
    order.slice(order.indexOf(name)).forEach((n) => { const node = qa(n); if (node) node.hidden = true; });
  }

  if (form) initForm();

  function initForm() {
    // A) identity --------------------------------------------------------------
    form.querySelector('[data-next="training"]').addEventListener('click', (e) => {
      e.currentTarget.closest('.actions').hidden = true;
      qa('identity').classList.add('is-done');
      setStep('identity', 'ok');
      setStep('training', 'active');
      reveal('training');
    });

    // B) training ----------------------------------------------------------------
    radioChips(qa('training').querySelector('.chips'), (chip) => {
      const ok = chip.dataset.training === 'yes';
      state.training = ok;
      document.getElementById('training-ko').hidden = ok;
      qa('training').classList.toggle('is-done', ok);
      qa('training').classList.toggle('is-failed', !ok);
      if (!ok) {
        setStep('training', 'ko', 'Training required');
        ['duration', 'details', 'directory'].forEach((s) => setStep(s, 'todo'));
        hideFrom('duration');
        return;
      }
      setStep('training', 'ok');
      if (!state.duration) setStep('duration', 'active');
      reveal('duration');
      applyPrefillDuration();
    });

    // C) duration ----------------------------------------------------------------
    radioChips(qa('duration').querySelector('.chips'), (chip) => {
      state.duration = chip.dataset.duration;
      qa('duration').classList.add('is-done');
      setStep('duration', 'ok');
      setStep('details', 'active');
      reveal('details');
      applyPrefillAnswers();
    });

    // D) usage details: one question unlocks the next ------------------------------
    const questions = [...form.querySelectorAll('.question')];
    questions.forEach((q, i) => {
      const next = () => {
        if (i + 1 < questions.length) {
          questions[i + 1].hidden = false;
        }
        refreshDetails();
      };
      if (q.dataset.type === 'select') {
        radioChips(q.querySelector('.chips'), (chip) => {
          state.answers[q.dataset.question] = chip.dataset.value;
          next();
        });
      } else {
        const input = q.querySelector('input, textarea');
        input.addEventListener('input', () => {
          state.answers[q.dataset.question] = input.value;
          updateCounter(q, input.value);
          if (questionValid(q)) next(); else refreshDetails();
        });
      }
    });
    form.querySelector('[data-next="directory"]').addEventListener('click', (e) => {
      e.currentTarget.closest('.actions').hidden = true;
      qa('details').classList.add('is-done');
      setStep('details', 'ok');
      setStep('directory', 'active');
      reveal('directory');
    });

    // W) directory -------------------------------------------------------------------
    document.getElementById('directory-check').addEventListener('click', (e) => busy(e.currentTarget, checkDirectory));

    // X/Y/Z) submit ------------------------------------------------------------------
    document.getElementById('submit-btn').addEventListener('click', submit);
  }

  function questionValid(q) {
    const v = (state.answers[q.dataset.question] || '').trim();
    const required = q.dataset.required === 'true';
    const min = Number(q.dataset.min || 0), max = Number(q.dataset.max || 0);
    if (!v) return !required;
    if (min && [...v].length < min) return false;
    if (max && [...v].length > max) return false;
    return true;
  }

  function updateCounter(q, value) {
    const counter = q.querySelector('[data-counter]');
    if (!counter) return;
    const min = Number(q.dataset.min || 0);
    const n = [...value.trim()].length;
    counter.textContent = n >= min ? `${n} characters ✓` : `At least ${min} characters (${n}/${min}).`;
  }

  /** Shows the "Continue" button of step D when every question is valid. */
  function refreshDetails() {
    const all = [...form.querySelectorAll('.question')];
    const ok = all.every(questionValid);
    form.querySelector('[data-details-next]').hidden = !ok || all.some((q) => q.hidden);
    if (!ok && qa('details').classList.contains('is-done')) {
      qa('details').classList.remove('is-done');
      setStep('details', 'active');
      hideFrom('directory');
    }
  }

  // Prefill from a previous request (draft, rejected or failed): values are
  // pre-selected but the user still walks through every step.
  function applyPrefillDuration() {
    if (!prefill || state.duration) return;
    const chip = qa('duration').querySelector(`[data-duration="${CSS.escape(prefill.duration || '')}"]`);
    if (chip) chip.setAttribute('aria-pressed', 'true');
  }
  function applyPrefillAnswers() {
    if (!prefill || !prefill.answers) return;
    const questions = [...form.querySelectorAll('.question')];
    questions.forEach((q) => {
      const v = prefill.answers[q.dataset.question];
      if (!v || state.answers[q.dataset.question]) return;
      if (q.dataset.type === 'select') {
        const chip = q.querySelector(`[data-value="${CSS.escape(v)}"]`);
        if (chip) chip.click();
      } else {
        const input = q.querySelector('input, textarea');
        input.value = v;
        input.dispatchEvent(new Event('input'));
      }
    });
  }

  // ---------------------------------------------------------------------------
  // 5) directory validation
  // ---------------------------------------------------------------------------
  async function checkDirectory() {
    const box = document.getElementById('directory-result');
    box.replaceChildren();
    let res;
    try {
      res = await api('POST', '/api/request/directory', {
        trainingAck: state.training, duration: state.duration, answers: state.answers,
      }, { timeoutMs: 20000 });
    } catch (err) {
      toast(err.message, 'ko');
      return;
    }
    state.requestId = res.requestId;
    applySteps(res.steps);
    // The answers are frozen once sent to the directory.
    ['identity', 'training', 'duration', 'details'].forEach((n) => qa(n).classList.add('form-readonly'));

    if (res.outcome === 'OK') {
      document.getElementById('directory-actions').hidden = true;
      qa('directory').classList.add('is-done');
      box.append(alertBox('ok', 'Eligible for automatic approval',
        el('div', { class: 'dir-chips' },
          el('span', { class: 'badge badge--plain' }, `Site ${res.site}`),
          el('span', { class: 'badge badge--plain' }, `Business unit ${res.businessUnit}`),
          el('span', { class: 'badge badge--plain' }, `GPS ${res.gpsCode}`))));
      if (res.poolExhausted) {
        document.getElementById('submit-intro').replaceChildren(alertBox('ko', 'Pool exhausted',
          el('p', {}, `Every license is assigned. Your validated request will join the waiting list at position #${res.nextPosition} and be served automatically.`)));
        document.getElementById('submit-btn').lastChild.textContent = ' Join the waiting list';
      }
      setStep('credentials', 'active');
      reveal('submit');
    } else if (res.outcome === 'FUNCTIONAL_KO') {
      document.getElementById('directory-actions').hidden = true;
      qa('directory').classList.add('is-failed');
      box.append(alertBox('ko', 'Automatic approval is not possible', el('p', {}, res.reason)),
        el('div', { class: 'actions' },
          el('button', { class: 'btn btn--primary', type: 'button', onclick: () => openEscalation(qa('escalation'), res.requestId) }, 'Escalate to an administrator'),
          el('button', { class: 'btn btn--ghost', type: 'button', onclick: () => withdraw(res.requestId) }, 'Withdraw my request')));
    } else {
      // Technical KO: the directory is unavailable and the process stops here.
      // Nothing was sent: the answers stay editable and the button stays
      // available to try again.
      ['identity', 'training', 'duration', 'details'].forEach((n) => qa(n).classList.remove('form-readonly'));
      box.append(alertBox('warn', 'Enterprise directory unavailable', el('p', {}, res.reason),
        el('p', { class: 'text-muted' }, 'Use the button below to try again.')));
    }
  }

  async function withdraw(requestId) {
    const { ok } = await confirm({ title: 'Withdraw your request?', text: 'You can submit a new request later.', confirmLabel: 'Withdraw', danger: true });
    if (!ok) return;
    try {
      await api('POST', `/api/request/${requestId}/exit`);
      location.reload();
    } catch (err) { toast(err.message, 'ko'); }
  }

  // ---------------------------------------------------------------------------
  // 6) escalation (form flow and "directory KO" card share the same markup)
  // ---------------------------------------------------------------------------
  function openEscalation(container, requestId) {
    if (container) container.hidden = false;
    const block = document.getElementById('escalation');
    block.hidden = false;
    setStep('admin', 'active', 'Fill in the escalation form');
    block.scrollIntoView({ behavior: 'smooth', block: 'center' });
    const btn = document.getElementById('escalate-btn');
    btn.onclick = () => busy(btn, () => escalate(requestId));
  }

  async function escalate(requestId) {
    const val = (id) => document.getElementById(id).value.trim();
    const body = { division: val('esc-division'), entity: val('esc-entity'), project: val('esc-project'), comment: val('esc-comment') };
    if (!body.division || !body.entity || !body.project) {
      toast('Division, entity and project are required.', 'warn');
      return;
    }
    try {
      const res = await api('POST', `/api/request/${requestId}/escalate`, body);
      document.querySelectorAll('#escalation input, #escalation textarea').forEach((i) => { i.readOnly = true; });
      document.getElementById('escalate-btn').closest('.actions').hidden = true;
      setStep('admin', 'active', 'Waiting for an administrator');
      const list = el('div', { class: 'admins' }, (res.admins || []).map((a) =>
        el('a', { href: `mailto:${a}` }, el('span', { class: 'avatar', 'aria-hidden': 'true' }, initials(a)), displayName(a))));
      document.getElementById('escalation-result').replaceChildren(alertBox('info', 'Sent to the administrators',
        el('p', {}, 'Your request is now waiting for a decision. You will receive an e-mail; you can also contact one of the administrators below.'), list));
      const choice = document.getElementById('dirko-choice');
      if (choice) choice.hidden = true;
      document.querySelectorAll('#directory-result .actions').forEach((a) => { a.hidden = true; });
    } catch (err) { toast(err.message, 'ko'); }
  }

  const displayName = (email) => email.split('@')[0].split(/[._-]/).filter(Boolean).map((p) => p[0].toUpperCase() + p.slice(1)).join(' ');
  const initials = (email) => displayName(email).split(' ').slice(0, 2).map((w) => w[0]).join('');

  // ---------------------------------------------------------------------------
  // X / Y / Z) credentials, license, e-mail — one call, loader, 10 s budget
  // ---------------------------------------------------------------------------
  async function submit() {
    const zone = document.getElementById('submit-zone');
    const result = document.getElementById('submit-result');
    zone.hidden = true;
    const msgs = [
      ['credentials', 'Granting your cloud credentials…'],
      ['license', 'Assigning your Gemini Code Assist license…'],
      ['email', 'Preparing your confirmation e-mail…'],
    ];
    const msg = el('div', { class: 'loader__msg' }, msgs[0][1]);
    const sub = el('div', { class: 'loader__sub' }, 'This usually takes a few seconds (10 s max).');
    result.replaceChildren(el('div', { class: 'loader', role: 'status' },
      el('div', { class: 'loader__ring', 'aria-hidden': 'true' }), msg, sub,
      el('div', { class: 'loader__bar', 'aria-hidden': 'true' }, el('span'))));
    let i = 0;
    setStep('credentials', 'active');
    const ticker = setInterval(() => {
      i = Math.min(i + 1, msgs.length - 1);
      msg.textContent = msgs[i][1];
      setStep(msgs[i][0], 'active');
    }, 2200);

    let res;
    try {
      res = await api('POST', `/api/request/${state.requestId}/submit`, undefined, { timeoutMs: (data.uxTimeoutMs || 10000) + 5000 });
    } catch (err) {
      clearInterval(ticker);
      if (err.timeout) {
        // The server buffers the request on its own timeout: just tell the user.
        result.replaceChildren(alertBox('warn', 'Still processing', el('p', {}, 'Google is slow to answer. Your request is saved and will be completed automatically: refresh this page in about 10 minutes.')));
        setStep('license', 'warn', 'Timeout — retry scheduled');
      } else {
        result.replaceChildren(alertBox('ko', 'The request could not be completed', el('p', {}, err.message)));
        zone.hidden = false;
      }
      return;
    }
    clearInterval(ticker);
    applySteps(res.steps);
    renderOutcome(result, res);
  }

  function renderOutcome(box, res) {
    const reload = el('div', { class: 'actions' }, el('a', { class: 'btn btn--primary', href: '/' }, 'Go to my license'));
    switch (res.outcome) {
      case 'ASSIGNED': {
        const a = res.assignment || {};
        box.replaceChildren(alertBox('ok', 'Your license is active 🎉',
          el('p', {}, `Valid until ${date(a.expiresAt)} (${relative(a.expiresAt)}). ${res.emailSent ? 'A confirmation e-mail with the VS Code set-up guide has been sent.' : 'The confirmation e-mail will be sent shortly.'}`)), reload);
        break;
      }
      case 'QUEUED':
        box.replaceChildren(alertBox('info', `You are #${res.queuePosition} in the waiting list`,
          el('p', {}, `${res.queueTotal} user(s) waiting. A license will be assigned automatically as soon as one is released; you will receive an e-mail.`)), reload);
        break;
      case 'BUFFERED':
        box.replaceChildren(alertBox('warn', 'Saved in the priority buffer',
          el('p', {}, res.message), el('p', {}, `Position #${res.bufferPosition} of ${res.bufferTotal}.`)), reload);
        break;
      default:
        box.replaceChildren(alertBox('ko', 'The request failed', el('p', {}, res.message)), reload);
    }
  }

  // ---------------------------------------------------------------------------
  // State-specific actions
  // ---------------------------------------------------------------------------

  // Case 0.2: resubmit after a rejection.
  const resubmit = document.getElementById('resubmit');
  if (resubmit) {
    resubmit.addEventListener('click', () => {
      document.getElementById('request-zone').hidden = false;
      STEP_ORDER.forEach((s) => setStep(s, s === 'identity' ? 'active' : s === 'admin' ? 'skipped' : 'todo'));
      resubmit.closest('.actions').hidden = true;
      document.getElementById('request-zone').scrollIntoView({ behavior: 'smooth' });
    });
  }

  // Directory KO card (request waiting for the user's choice).
  const dirko = document.getElementById('dirko-card');
  if (dirko) {
    const id = Number(dirko.dataset.request);
    dirko.querySelector('[data-action="escalate"]').addEventListener('click', () => openEscalation(null, id));
    dirko.querySelector('[data-action="exit"]').addEventListener('click', () => withdraw(id));
  }

  // Case 1: leave the waiting list.
  const leave = document.getElementById('leave-queue');
  if (leave) {
    leave.addEventListener('click', async () => {
      const { ok } = await confirm({ title: 'Leave the waiting list?', text: 'You will lose your position. You can request a license again later.', confirmLabel: 'Leave', danger: true });
      if (!ok) return;
      try { await api('POST', '/api/queue/leave'); location.reload(); } catch (err) { toast(err.message, 'ko'); }
    });
  }
})();
