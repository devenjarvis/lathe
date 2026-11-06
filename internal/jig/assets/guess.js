// Guess first: turn the lettered option list into pressable choices with a
// "Lock it in" step, and reveal the answer and explanation in place. Without
// this script the list stays a list and the answer waits in a <details>.
window.LatheJig.register('guess', function(frame, api){
  var body = frame.querySelector('.jig-body');
  var list = frame.querySelector('.jig-guess-options');
  var reveal = frame.querySelector('.jig-guess-reveal');
  var explain = reveal && reveal.querySelector('.jig-guess-explain');
  if (!body || !list || !reveal || !explain) return;

  var items = Array.prototype.slice.call(list.querySelectorAll('.jig-guess-option'));
  var right = items.findIndex(function(li){ return li.hasAttribute('data-correct'); });
  if (right < 0) return;
  var picked = -1;
  var locked = false;

  var buttons = items.map(function(li, i){
    var text = api.el('span', {'class': 'jig-guess-text'});
    while (li.firstChild) text.appendChild(li.firstChild);
    var btn = api.el('button', {type: 'button', 'class': 'jig-guess-choice', 'aria-pressed': 'false'}, [
      api.el('span', {'class': 'jig-guess-key', 'aria-hidden': 'true'}, [li.getAttribute('data-key') || '']),
      text,
      api.el('span', {'class': 'visually-hidden jig-guess-mark'})
    ]);
    btn.addEventListener('click', function(){
      if (locked) return;
      picked = i;
      sync();
    });
    li.appendChild(btn);
    return btn;
  });

  var lock = api.el('button', {type: 'button', 'class': 'btn btn-primary jig-guess-lock', disabled: ''}, ['Lock it in']);
  var actions = api.el('div', {'class': 'jig-guess-actions'}, [
    lock,
    api.el('span', {'class': 'jig-guess-hint'}, ['No penalty for being wrong. Guessing is the point.'])
  ]);
  // The live region exists (empty) before it is filled, so screen readers
  // announce the verdict when it arrives.
  var verdict = api.el('div', {'class': 'jig-guess-verdict', role: 'status'});
  // The explanation stays in the document, hidden, until the reveal: page-wide
  // passes that run after mount (KaTeX typesetting math) must still reach it.
  var held = api.el('div', {hidden: ''}, [explain]);
  reveal.parentNode.replaceChild(actions, reveal);
  body.appendChild(verdict);
  body.appendChild(held);

  lock.addEventListener('click', function(){
    if (picked < 0) return;
    locked = true;
    var key = items[right].getAttribute('data-key');
    var lead = picked === right ? 'Right.' : 'Not quite. The answer is ' + key + '.';
    verdict.appendChild(api.el('p', {'class': 'jig-guess-result'}, [api.el('strong', {}, [lead])]));
    verdict.appendChild(explain);
    held.remove();
    actions.hidden = true;
    sync();
    buttons[picked].focus();
  });

  function sync(){
    buttons.forEach(function(btn, i){
      btn.setAttribute('aria-pressed', String(i === picked));
      btn.classList.toggle('is-picked', i === picked && !locked);
      btn.classList.toggle('is-right', locked && i === right);
      btn.classList.toggle('is-wrong', locked && i === picked && i !== right);
      if (locked) btn.setAttribute('aria-disabled', 'true');
      var mark = btn.querySelector('.jig-guess-mark');
      mark.textContent = !locked ? '' : i === right ? ', correct answer' : i === picked ? ', your answer' : '';
    });
    lock.disabled = picked < 0;
    frame.classList.toggle('is-locked', locked);
  }
});
