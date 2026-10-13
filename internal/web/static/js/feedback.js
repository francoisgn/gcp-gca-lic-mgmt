/* Feedback page: category chips and submission. */
'use strict';

(() => {
  const { api, toast, radioChips, busy } = GCA;
  const form = document.getElementById('feedback-form');
  let category = 'IMPROVEMENT';
  radioChips(form.querySelector('.chips'), (chip) => { category = chip.dataset.category; });

  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const message = form.querySelector('#feedback-message').value.trim();
    if (message.length < 10) {
      toast('Please write at least 10 characters.', 'warn');
      return;
    }
    busy(form.querySelector('button[type="submit"]'), async () => {
      try {
        const res = await api('POST', '/api/feedback', { category, message, page: '/feedback' });
        toast(res.trackerKey ? `Thank you! Work item ${res.trackerKey} created.` : 'Thank you! Your feedback is saved.', 'ok');
        setTimeout(() => location.reload(), 1200);
      } catch (err) { toast(err.message, 'ko'); }
    });
  });
})();
