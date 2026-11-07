// The reading page's bridges between the tutorial, the agent panel, and the
// reader's editor and terminal (issue #145):
//
//  - Select text in the article: a small toolbar offers Ask (⌘L; the
//    selection goes to the agent panel's composer as a quote), Explain it
//    more simply (asks right away) and Copy.
//  - Right-click a listing (or press the context-menu key on it): Copy, Run
//    in Terminal (commands), Compare with my <path> and Open <path> in
//    <editor> (listings whose fence names a path), Ask about this code, and
//    Check my version of this step. Shift+right-click keeps the browser's
//    own menu.
//  - Command blocks get Run in Terminal beside Copy.
//  - The masthead's workspace button: Open in editor, Open in Terminal, Show
//    in Finder.
//
// Launching anything goes through same-origin POSTs to the server
// (native_workspace.go), which only a desktop shell answers; in plain
// `lathe serve` (cfg.launch false) those items are not shown, and Ask, Copy,
// Compare and Check still work.
(function () {
  var cfgEl = document.getElementById('latheReadingTools');
  if (!cfgEl) return;
  var cfg = {};
  try { cfg = JSON.parse(cfgEl.textContent) || {}; } catch (e) { return; }
  var root = document.documentElement;
  var mac = /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent || '');
  var popout = root.hasAttribute('data-agent-popout');
  var panel = document.getElementById('agentPanel');
  var poll = document.getElementById('statusPoll');
  var slug = (panel && panel.getAttribute('data-slug')) || (poll && poll.getAttribute('data-slug')) || '';
  var main = document.querySelector('main');

  function kbd(macCombo, other) { return mac ? macCombo : other; }
  document.querySelectorAll('#selectionBar [data-kbd]').forEach(function (k) { k.textContent = kbd('⌘L', 'Ctrl+L'); });

  // ---- Small helpers ----------------------------------------------------
  var toast = document.createElement('div');
  toast.className = 'rt-toast';
  toast.setAttribute('role', 'status');
  toast.setAttribute('aria-live', 'polite');
  toast.hidden = true;
  document.body.appendChild(toast);
  var toastTimer = null;
  function say(msg, error) {
    toast.textContent = msg;
    toast.classList.toggle('is-error', !!error);
    toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { toast.hidden = true; }, error ? 9000 : 7000);
  }
  function postJSON(url, body) {
    return fetch(url, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)})
      .then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error((t || ('HTTP ' + r.status)).trim()); });
        return r.status === 204 ? null : r.json();
      });
  }
  function copyText(text, what) {
    if (!navigator.clipboard || !navigator.clipboard.writeText) {
      say('Copy failed. Select the text and copy it by hand.', true);
      return Promise.resolve(false);
    }
    return navigator.clipboard.writeText(text).then(function () {
      say((what || 'Text') + ' copied.');
      return true;
    }, function () { say('Copy failed. Select the text and copy it by hand.', true); return false; });
  }
  function hasWorkspace() { return !!cfg.workspace; }
  function wsName() { return cfg.workspaceName || 'your workspace'; }
  function blockCode(block) {
    var c = block.querySelector('pre code, pre samp');
    return c ? c.textContent.replace(/\n+$/, '') : '';
  }
  function blockKind(block) {
    if (block.classList.contains('tutorial-block-command')) return 'command';
    if (block.classList.contains('tutorial-block-output')) return 'output';
    return 'code';
  }
  function blockLang(block) {
    var l = block.querySelector('.tutorial-block-label');
    var t = l ? l.textContent : '';
    var i = t.lastIndexOf('·');
    return i >= 0 ? t.slice(i + 1).trim() : '';
  }
  // Where a node is: "Part 2 · Envelope" (the section heading above it).
  function placeOf(node) {
    var place = document.getElementById('agentPlace');
    var base = place ? (place.getAttribute('data-part-label') || '') : '';
    var head = null;
    if (main && node) {
      main.querySelectorAll('h2[id]').forEach(function (h) {
        if (h.compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING) head = h;
      });
    }
    var sec = head ? head.textContent.trim() : '';
    return [base, sec].filter(Boolean).join(' · ');
  }

  // ---- The composer's quote -------------------------------------------
  var MAX_QUOTE = 3000;
  var qBox = document.getElementById('askQuote');
  var qText = document.getElementById('askQuoteText');
  var qLabel = document.getElementById('askQuoteLabel');
  var input = document.getElementById('askInput');
  var askForm = document.getElementById('askForm');
  var quote = null;
  function clip(t) { return t.length > MAX_QUOTE ? t.slice(0, MAX_QUOTE) + '\n…' : t; }
  function setQuote(q) {
    if (!qBox) return;
    q.text = clip(q.text);
    quote = q;
    qLabel.textContent = q.label ? 'Quoting ' + q.label : 'Quoting';
    qText.textContent = q.text;
    qBox.hidden = false;
  }
  function clearQuote() {
    quote = null;
    if (qBox) { qBox.hidden = true; qText.textContent = ''; }
  }
  function formatQuote(q) {
    var head = q.label ? 'From ' + q.label + ':\n' : '';
    if (q.kind === 'code') {
      return head + '```' + (q.lang || '') + '\n' + q.text + '\n```';
    }
    return head + q.text.split('\n').map(function (l) { return '> ' + l; }).join('\n');
  }
  if (qBox) {
    document.getElementById('askQuoteRemove').addEventListener('click', function () {
      clearQuote();
      if (input) { try { input.focus(); } catch (e) {} }
    });
  }
  window.latheAskQuote = {
    set: setQuote,
    clear: clearQuote,
    // wrap puts the quote ahead of the question, once.
    wrap: function (question) {
      if (!quote) return question;
      var q = quote;
      clearQuote();
      return formatQuote(q) + '\n\n' + question;
    }
  };
  // askWith quotes q in the composer and opens the panel; with a question it
  // sends straight away.
  function askWith(q, question) {
    if (!window.latheAgentPanel || !input) return;
    setQuote(q);
    window.latheAgentPanel.open({focus: question ? 'none' : 'ask', session: true});
    if (question) {
      input.value = question;
      if (typeof askForm.requestSubmit === 'function') askForm.requestSubmit();
    } else {
      setTimeout(function () { try { input.focus(); } catch (e) {} }, 60);
    }
  }

  // ---- Selection toolbar ----------------------------------------------
  var bar = document.getElementById('selectionBar');
  var barFor = null; // the selection info the bar shows for
  function selectionInfo() {
    var sel = window.getSelection ? window.getSelection() : null;
    if (!sel || sel.isCollapsed || !sel.rangeCount || !main) return null;
    var range = sel.getRangeAt(0);
    var node = range.commonAncestorContainer;
    var el = node.nodeType === 1 ? node : node.parentElement;
    if (!el || !main.contains(el)) return null;
    if (el.closest('input, textarea, select, button, [contenteditable="true"], dialog')) return null;
    var text = sel.toString().replace(/\s+$/, '');
    if (!text.trim()) return null;
    var block = el.closest('.tutorial-block');
    return {text: text, range: range, node: el, block: block};
  }
  function quoteFromSelection(info) {
    var place = placeOf(info.node);
    if (info.block) {
      var p = info.block.getAttribute('data-path');
      return {kind: 'code', lang: blockLang(info.block), text: info.text,
        label: [place, p].filter(Boolean).join(' · ')};
    }
    return {kind: 'text', text: info.text.trim(), label: place};
  }
  function hideBar() { if (bar) bar.hidden = true; barFor = null; }
  function placeBar() {
    if (!bar || !barFor) return;
    var r = barFor.range.getBoundingClientRect();
    if (!r || (r.width === 0 && r.height === 0)) { hideBar(); return; }
    bar.hidden = false;
    var bw = bar.offsetWidth, bh = bar.offsetHeight;
    var top = document.getElementById('topBar');
    var minTop = (top ? top.getBoundingClientRect().bottom : 0) + 6;
    var y = r.top - bh - 8;
    if (y < minTop) y = r.bottom + 8;
    var x = r.left + r.width / 2 - bw / 2;
    x = Math.max(8, Math.min(x, window.innerWidth - bw - 8));
    bar.style.top = Math.round(y) + 'px';
    bar.style.left = Math.round(x) + 'px';
  }
  var mouseDown = false;
  var selTimer = null;
  function checkSelection() {
    if (popout) return;
    var info = selectionInfo();
    if (!info) { hideBar(); return; }
    barFor = info;
    placeBar();
  }
  if (bar && main && !popout) {
    document.addEventListener('mousedown', function (ev) {
      if (bar.contains(ev.target)) { ev.preventDefault(); return; } // keep the selection
      mouseDown = true;
    });
    document.addEventListener('mouseup', function () { mouseDown = false; setTimeout(checkSelection, 0); });
    document.addEventListener('selectionchange', function () {
      clearTimeout(selTimer);
      if (mouseDown) { hideBar(); return; }
      selTimer = setTimeout(checkSelection, 180);
    });
    window.addEventListener('scroll', function () { if (barFor) placeBar(); }, {passive: true});
    window.addEventListener('resize', function () { if (barFor) placeBar(); });
    document.addEventListener('keydown', function (ev) {
      if (ev.key === 'Escape' && !bar.hidden) hideBar();
    });
    bar.addEventListener('click', function (ev) {
      var b = ev.target.closest('[data-sel]');
      if (!b || !barFor) return;
      var info = barFor;
      var what = b.getAttribute('data-sel');
      if (what === 'copy') {
        copyText(info.text, 'Selection');
      } else if (what === 'ask') {
        askWith(quoteFromSelection(info));
      } else if (what === 'simpler') {
        askWith(quoteFromSelection(info), 'Explain this more simply.');
      }
      hideBar();
    });
  }

  // ⌘L (agent.ask-selection, via shortcuts.js): quote the selection, else
  // the listing that has focus, else just open the composer.
  function askSelection() {
    var info = selectionInfo();
    if (info) { askWith(quoteFromSelection(info)); hideBar(); return; }
    var a = document.activeElement;
    var block = a && main && main.contains(a) && a.closest ? a.closest('.tutorial-block') : null;
    if (block) { askAboutBlock(block); return; }
    if (window.latheAgentPanel) window.latheAgentPanel.open({focus: 'ask', session: true});
  }
  function askAboutBlock(block) {
    var p = block.getAttribute('data-path');
    askWith({kind: 'code', lang: blockLang(block), text: blockCode(block),
      label: [placeOf(block), p || (blockKind(block) === 'command' ? 'command' : blockKind(block) === 'output' ? 'expected output' : '')].filter(Boolean).join(' · ')});
  }

  // ---- Menus (the listing menu and the workspace menu) ----------------
  // items: {label, desc, disabled, run} or 'sep'. Roving focus: ↑/↓, Home,
  // End; Enter/Space picks; Escape or Tab closes.
  function fillMenu(menu, items, onPick) {
    menu.textContent = '';
    items.forEach(function (it) {
      if (it === 'sep') {
        if (menu.lastChild && !menu.lastChild.classList.contains('agent-menu-sep')) {
          var s = document.createElement('div');
          s.className = 'agent-menu-sep';
          s.setAttribute('role', 'separator');
          menu.appendChild(s);
        }
        return;
      }
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'agent-menu-item';
      b.setAttribute('role', 'menuitem');
      b.tabIndex = -1;
      var row = document.createElement('span');
      row.className = 'agent-menu-row';
      var l = document.createElement('span');
      l.className = 'agent-menu-label';
      l.textContent = it.label;
      row.appendChild(l);
      if (it.key) {
        var k = document.createElement('kbd');
        k.className = 'agent-kbd agent-menu-key';
        k.setAttribute('aria-hidden', 'true');
        k.textContent = it.key;
        row.appendChild(k);
      }
      b.appendChild(row);
      if (it.desc) {
        var d = document.createElement('span');
        d.className = 'agent-menu-desc';
        d.textContent = it.desc;
        b.appendChild(d);
      }
      if (it.disabled) b.setAttribute('aria-disabled', 'true');
      b.addEventListener('click', function () {
        if (it.disabled) return;
        onPick();
        it.run();
      });
      menu.appendChild(b);
    });
    var last = menu.lastChild;
    if (last && last.classList.contains('agent-menu-sep')) menu.removeChild(last);
  }
  function menuKeys(menu, close) {
    menu.addEventListener('keydown', function (ev) {
      var items = Array.prototype.slice.call(menu.querySelectorAll('[role="menuitem"]'));
      var i = items.indexOf(document.activeElement);
      var n = null;
      if (ev.key === 'ArrowDown') n = (i + 1) % items.length;
      else if (ev.key === 'ArrowUp') n = (i - 1 + items.length) % items.length;
      else if (ev.key === 'Home') n = 0;
      else if (ev.key === 'End') n = items.length - 1;
      else if (ev.key === 'Escape') { ev.preventDefault(); close(true); return; }
      else if (ev.key === 'Tab') { close(false); return; }
      if (n === null) return;
      ev.preventDefault();
      items[n].focus();
    });
  }

  // ---- Launchers ------------------------------------------------------
  function needWorkspace() {
    say('Set a workspace folder first: it is where Lathe opens your editor and terminal.');
    if (window.latheAgentPanel && window.latheAgentPanel.editWorkspace) window.latheAgentPanel.editWorkspace();
  }
  function openEditor(path) {
    if (!hasWorkspace()) { needWorkspace(); return; }
    postJSON('/-/native/open-editor', {slug: slug, path: path || ''})
      .then(function () { say('Opened ' + (path || wsName()) + (cfg.editor ? ' in ' + cfg.editor : '') + '.'); })
      .catch(function (err) { say(err.message, true); });
  }
  function openSettings(tab) {
    if (window.lathePrefs && window.lathePrefs.openSettings) window.lathePrefs.openSettings(tab);
    else location.href = '/-/settings#' + tab;
  }
  function terminalNote(res, command) {
    var app = res.app || cfg.terminal || 'Terminal';
    if (!command) return 'Opened ' + app + ' in ' + wsName() + '.';
    if (res.typed) return 'Opened ' + app + ' in ' + wsName() + ' with the command typed in. Press Return there to run it.';
    if (res.history) return 'Opened ' + app + ' in ' + wsName() + '. The command is on your clipboard, and ↑ there brings it back.';
    if (res.clipboard) return 'Opened ' + app + ' in ' + wsName() + '. It can’t take typed text from Lathe, so the command is on your clipboard: paste it, then press Return.';
    return 'Opened ' + app + ' in ' + wsName() + '.';
  }
  function openTerminal(command, btn) {
    if (!hasWorkspace()) { needWorkspace(); return; }
    if (btn) { btn.disabled = true; btn.setAttribute('aria-busy', 'true'); }
    postJSON('/-/native/terminal', {slug: slug, command: command || ''})
      .then(function (res) { say(terminalNote(res || {}, command)); })
      .catch(function (err) { say(err.message, true); })
      .then(function () { if (btn) { btn.disabled = false; btn.removeAttribute('aria-busy'); } });
  }
  function reveal() {
    if (!hasWorkspace()) { needWorkspace(); return; }
    postJSON('/-/native/reveal', {what: 'workspace', slug: slug})
      .catch(function (err) { say(err.message, true); });
  }

  // ---- Compare with my <path> ------------------------------------------
  var dlg = document.getElementById('compareDialog');
  function compare(block) {
    var path = block.getAttribute('data-path');
    if (!path) return;
    if (!hasWorkspace()) { needWorkspace(); return; }
    var code = blockCode(block);
    postJSON('/-/compare/' + encodeURIComponent(slug), {path: path, code: code})
      .then(function (res) { showCompare(block, path, res); })
      .catch(function (err) { say(err.message, true); });
  }
  function showCompare(block, path, res) {
    if (!dlg || typeof dlg.showModal !== 'function') return;
    var title = document.getElementById('compareTitle');
    var note = document.getElementById('compareNote');
    var body = document.getElementById('compareBody');
    var legend = document.getElementById('compareLegend');
    var foot = document.getElementById('compareFoot');
    title.textContent = 'Your ' + path + ' and this listing';
    body.textContent = '';
    foot.textContent = '';
    var diffText = [];
    if (res.missing) {
      note.textContent = 'There’s no ' + path + ' in ' + wsName() + ' yet. This listing is what goes in it.';
      body.hidden = true; legend.hidden = true;
    } else if (res.same) {
      note.textContent = 'Your ' + path + ' matches this listing.' + extent(res);
      body.hidden = true; legend.hidden = true;
    } else {
      var changed = (res.lines || []).filter(function (l) { return l.op === 'tutorial' || l.op === 'yours'; }).length;
      note.textContent = changed + ' line' + (changed === 1 ? '' : 's') + ' differ.' + extent(res);
      legend.hidden = false;
      body.hidden = false;
      (res.lines || []).forEach(function (l) {
        l.text = l.text || '';
        var row = document.createElement('span');
        row.className = 'cmp-line cmp-' + l.op;
        var mark = l.op === 'tutorial' ? '+' : l.op === 'yours' ? '−' : ' ';
        if (l.op === 'gap') {
          row.textContent = '⋯ ' + l.n + ' matching line' + (l.n === 1 ? '' : 's');
          diffText.push('@@ ' + l.n + ' matching lines @@');
        } else {
          row.textContent = mark + ' ' + l.text;
          diffText.push((l.op === 'tutorial' ? '+' : l.op === 'yours' ? '-' : ' ') + l.text);
        }
        if (l.op === 'tutorial') row.setAttribute('aria-label', 'Only in the tutorial: ' + l.text);
        if (l.op === 'yours') row.setAttribute('aria-label', 'Only in yours: ' + l.text);
        body.appendChild(row);
      });
    }
    function action(text, cls, fn) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'btn btn-sm ' + cls;
      b.textContent = text;
      b.addEventListener('click', function () { dlg.close(); fn(); });
      foot.appendChild(b);
    }
    if (diffText.length) {
      action('Ask about the difference', 'btn-primary', function () {
        askWith({kind: 'code', lang: 'diff', label: placeOf(block) + ' · your ' + path + ' (−) vs. the listing (+)', text: diffText.join('\n')});
      });
    }
    if (cfg.launch && !res.missing) action(cfg.editor ? 'Open in ' + cfg.editor : 'Open ' + path, 'btn-ghost', function () { openEditor(path); });
    action('Close', 'btn-ghost', function () {});
    dlg.showModal();
    try { (diffText.length ? body : foot.lastChild).focus(); } catch (e) {}
  }
  function extent(res) {
    var bits = [];
    if (res.above) bits.push(res.above + ' line' + (res.above === 1 ? '' : 's') + ' above');
    if (res.below) bits.push(res.below + ' below');
    return bits.length ? ' Showing the part of your file the listing covers; ' + bits.join(' and ') + ' are not shown.' : '';
  }
  if (dlg) {
    document.getElementById('compareClose').addEventListener('click', function () { dlg.close(); });
    dlg.addEventListener('click', function (ev) { if (ev.target === dlg) dlg.close(); });
  }

  // ---- Listing context menu --------------------------------------------
  var lmenu = document.getElementById('listingMenu');
  var lmenuReturn = null;
  function closeListingMenu(refocus) {
    if (!lmenu || lmenu.hidden) return;
    lmenu.hidden = true;
    if (refocus && lmenuReturn) { try { lmenuReturn.focus(); } catch (e) {} }
  }
  function checkItem() {
    var cw = document.getElementById('checkWork');
    if (!cw || !window.latheAgentPanel) return null;
    var p = window.latheAgentPanel;
    var why = '';
    if (!hasWorkspace() && cw.getAttribute('data-has-workspace') !== '1') why = 'Needs a workspace. Set one in the agent panel.';
    else if (p.canRun && !p.canRun('check')) why = 'Can’t run right now.';
    return {label: 'Check my version of this step', desc: why || 'Your agent runs this part’s checkpoint on a copy of your code.',
      disabled: !!why, run: function () { p.open({focus: 'none'}); p.runAction('check'); }};
  }
  function listingItems(block) {
    var kind = blockKind(block);
    var code = blockCode(block);
    var path = block.getAttribute('data-path');
    var sel = selectionInfo();
    var selHere = sel && block.contains(sel.node) ? sel.text : '';
    var items = [];
    items.push({label: selHere ? 'Copy selection' : 'Copy', key: selHere ? kbd('⌘C', 'Ctrl+C') : '',
      run: function () { copyText(selHere || code, selHere ? 'Selection' : (kind === 'command' ? 'Command' : 'Code')); }});
    if (kind === 'command' && cfg.launch) {
      items.push({label: 'Run in ' + (cfg.terminal || 'Terminal'),
        desc: hasWorkspace() ? 'Opens it in ' + wsName() + ' with the command typed in. You press Return.' : 'Set a workspace first.',
        disabled: !hasWorkspace(), run: function () { openTerminal(code); }});
    }
    if (path) {
      items.push('sep');
      items.push({label: 'Compare with my ' + path, desc: hasWorkspace() ? '' : 'Set a workspace first.',
        disabled: !hasWorkspace(), run: function () { compare(block); }});
      if (cfg.launch) {
        items.push({label: 'Open ' + path + (cfg.editor ? ' in ' + cfg.editor : ''),
          desc: hasWorkspace() ? '' : 'Set a workspace first.',
          disabled: !hasWorkspace(), run: function () { openEditor(path); }});
      }
    }
    if (window.latheAgentPanel) {
      items.push('sep');
      items.push({label: kind === 'command' ? 'Ask about this command' : kind === 'output' ? 'Ask about this output' : 'Ask about this code',
        key: kbd('⌘L', 'Ctrl+L'), run: function () { askAboutBlock(block); }});
      if (kind === 'code') {
        var ci = checkItem();
        if (ci) items.push(ci);
      }
    }
    return items;
  }
  function openListingMenu(block, x, y) {
    hideBar();
    closeWsMenu(false);
    lmenuReturn = block.querySelector('pre') || document.activeElement;
    fillMenu(lmenu, listingItems(block), function () { closeListingMenu(true); });
    lmenu.hidden = false;
    var w = lmenu.offsetWidth, h = lmenu.offsetHeight;
    lmenu.style.left = Math.round(Math.max(8, Math.min(x, window.innerWidth - w - 8))) + 'px';
    lmenu.style.top = Math.round(Math.max(8, Math.min(y, window.innerHeight - h - 8))) + 'px';
    var first = lmenu.querySelector('[role="menuitem"]');
    if (first) first.focus();
  }
  if (lmenu && main && !popout) {
    menuKeys(lmenu, closeListingMenu);
    document.addEventListener('contextmenu', function (ev) {
      if (ev.shiftKey) return; // the browser's own menu
      var block = ev.target.closest && ev.target.closest('.tutorial-block');
      if (!block || !main.contains(block) || ev.target.closest('button, a, input, textarea')) return;
      ev.preventDefault();
      var x = ev.clientX, y = ev.clientY;
      if (!x && !y) { // the keyboard's menu key: under the listing's label
        var r = (block.querySelector('pre') || block).getBoundingClientRect();
        x = r.left + 16; y = r.top + 16;
      }
      openListingMenu(block, x, y);
    });
    document.addEventListener('mousedown', function (ev) { if (!lmenu.hidden && !lmenu.contains(ev.target)) closeListingMenu(false); });
    window.addEventListener('scroll', function () { closeListingMenu(false); }, {passive: true});
    window.addEventListener('blur', function () { closeListingMenu(false); });
  }

  // ---- Command blocks: Copy | Run in Terminal --------------------------
  if (cfg.launch && main) {
    main.querySelectorAll('.tutorial-block-command .copy-command').forEach(function (copy) {
      var block = copy.closest('.tutorial-block');
      var wrap = document.createElement('span');
      wrap.className = 'command-actions';
      copy.parentNode.insertBefore(wrap, copy);
      wrap.appendChild(copy);
      copy.textContent = 'Copy';
      var run = document.createElement('button');
      run.type = 'button';
      run.className = 'btn btn-ghost btn-sm run-command';
      run.textContent = 'Run in ' + (cfg.terminal || 'Terminal');
      run.title = 'Opens ' + (cfg.terminal || 'Terminal') + ' in your workspace with the command typed in. You press Return.';
      run.addEventListener('click', function () { openTerminal(blockCode(block), run); });
      wrap.appendChild(run);
    });
  }

  // ---- The masthead's workspace menu ----------------------------------
  var wsWrap = document.getElementById('wsMenuWrap');
  var wsBtn = document.getElementById('wsMenuButton');
  var wsMenu = document.getElementById('wsMenu');
  function closeWsMenu(refocus) {
    if (!wsMenu || wsMenu.hidden) return;
    wsMenu.hidden = true;
    wsBtn.setAttribute('aria-expanded', 'false');
    if (refocus) { try { wsBtn.focus(); } catch (e) {} }
  }
  function wsItems() {
    var items = [];
    if (cfg.launch) {
      items.push(cfg.editor
        ? {label: 'Open in ' + cfg.editor, run: function () { openEditor(''); }}
        : {label: 'Open in editor…', desc: 'Choose your editor in Settings › Reading.', run: function () { openSettings('reading'); }});
      items.push({label: 'Open in ' + (cfg.terminal || 'Terminal'), run: function () { openTerminal(''); }});
    }
    if (cfg.reveal) {
      if (items.length) items.push('sep');
      items.push({label: cfg.revealLabel || 'Show in Finder', run: reveal});
    }
    return items;
  }
  if (wsBtn && wsMenu) {
    menuKeys(wsMenu, closeWsMenu);
    wsBtn.addEventListener('click', function () {
      if (!wsMenu.hidden) { closeWsMenu(false); return; }
      closeListingMenu(false);
      fillMenu(wsMenu, wsItems(), function () { closeWsMenu(true); });
      wsMenu.hidden = false;
      wsBtn.setAttribute('aria-expanded', 'true');
      var first = wsMenu.querySelector('[role="menuitem"]');
      if (first) first.focus();
    });
    wsBtn.addEventListener('keydown', function (ev) {
      if (ev.key === 'ArrowDown' && wsMenu.hidden) { ev.preventDefault(); wsBtn.click(); }
    });
    document.addEventListener('mousedown', function (ev) { if (!wsWrap.contains(ev.target)) closeWsMenu(false); });
  }
  // The agent panel saved a new workspace.
  document.addEventListener('lathe-workspace', function (ev) {
    var ws = ev.detail || {};
    if (!ws.path) return;
    cfg.workspace = ws.path;
    cfg.workspaceName = ws.path.replace(/[\\/]+$/, '').split(/[\\/]/).pop();
    if (wsWrap) {
      wsWrap.hidden = false;
      document.getElementById('wsMenuName').textContent = cfg.workspaceName;
      wsBtn.title = ws.path;
    }
    if (window.latheShortcuts && window.latheShortcuts.refresh) window.latheShortcuts.refresh();
  });

  // For shortcuts.js (⌘L and the Tutorial menu's workspace items).
  window.latheReadingTools = {
    askSelection: askSelection,
    canLaunch: function () { return !!cfg.launch && hasWorkspace(); },
    canReveal: function () { return !!cfg.reveal && hasWorkspace(); },
    openEditor: function () { if (cfg.editor) openEditor(''); else openSettings('reading'); },
    openTerminal: function () { openTerminal(''); },
    reveal: reveal
  };
})();
