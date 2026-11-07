// Go to anything (#144): the palette over the current page. ⌘K / Ctrl+K
// (the registry's library.goto, run by shortcuts.js or the desktop menu) and
// the masthead search button ([data-palette-open]) open it.
//
// It searches GET /-/index (tutorials, their parts and h2/h3 sections, tags,
// shelves) in the browser and adds actions: the registry commands this page
// can run (window.latheShortcuts.available), a tag view, a /lathe command for
// a new tutorial and, on a reading page with the agent panel, asking the
// agent about the query in this part. Typing ">" first lists actions only.
//
// The input is a combobox over a grouped listbox: the options never take
// focus; aria-activedescendant names the active one.
(function () {
  var dlg = document.getElementById('palette');
  var input = document.getElementById('paletteInput');
  var listEl = document.getElementById('paletteList');
  if (!dlg || !input || !listEl || typeof dlg.showModal !== 'function') return;
  // The agent pop-out window is the panel alone: no palette there.
  if (/[?&]agent=popout(&|$)/.test(location.search)) return;

  var root = document.documentElement;
  var mac = /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent || '');
  var modeEl = document.getElementById('paletteMode');
  var statusEl = document.getElementById('paletteStatus');
  var closeBtn = document.getElementById('paletteClose');

  var index = null;       // the last /-/index body
  var loading = null;     // the in-flight fetch
  var loadFailed = false;
  var items = [];         // the options as rendered, in order
  var active = -1;
  var returnFocus = null;
  var statusTimer = 0;

  // Key hints are written for macOS; elsewhere ⌘ reads Ctrl+.
  function kbdText(t) {
    if (mac) return t;
    var mods = [];
    if (t.indexOf('⌃') >= 0) mods.push('Ctrl');
    if (t.indexOf('⌥') >= 0) mods.push('Alt');
    if (t.indexOf('⌘') >= 0 && mods.indexOf('Ctrl') < 0) mods.push('Ctrl');
    if (t.indexOf('⇧') >= 0) mods.push('Shift');
    var key = t.replace(/[⌃⌥⌘⇧]/g, '');
    if (key === '↩') key = 'Enter';
    return mods.concat([key]).join('+');
  }
  document.querySelectorAll('[data-palette-kbd]').forEach(function (k) {
    k.textContent = kbdText(k.textContent);
  });

  // The registry's combo ("Cmd+Shift+K") as a menu shows it (shortcuts.DisplayCombo).
  function displayCombo(combo) {
    if (!combo) return '';
    var i = combo.lastIndexOf('+', combo.length - 2);
    var mods = i < 0 ? [] : combo.slice(0, i).split('+');
    var key = i < 0 ? combo : combo.slice(i + 1);
    if (!mac) return mods.concat([key]).join('+');
    var out = '';
    [['Ctrl', '⌃'], ['Option', '⌥'], ['Shift', '⇧'], ['Cmd', '⌘']].forEach(function (m) {
      if (mods.indexOf(m[0]) >= 0) out += m[1];
    });
    return out + (key === 'Enter' ? '↩' : key === 'Home' ? '↖' : key);
  }

  function load() {
    if (loading) return loading;
    loading = fetch('/-/index', {headers: {Accept: 'application/json'}})
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
      .then(function (data) { index = data; loadFailed = false; })
      .catch(function () { loadFailed = true; })
      .then(function () { loading = null; if (dlg.open) render(); });
    return loading;
  }

  // ---- Scoring ------------------------------------------------------------
  // score is how well text matches one query token: an exact match, a
  // prefix, the start of a word, anywhere, then the token's letters in order
  // (a loose fuzzy match). 0 is no match.
  function score(text, tok) {
    if (!text) return 0;
    var t = text.toLowerCase();
    if (t === tok) return 100;
    var i = t.indexOf(tok);
    if (i === 0) return 80;
    if (i > 0) {
      var before = t.charAt(i - 1);
      return (/[\s\-_/.·:(]/.test(before) ? 60 : 40) - Math.min(i, 20) / 2;
    }
    // A word that shares a stem with the token: "parser" finds "parsing".
    if (tok.length >= 5) {
      var words = t.split(/[^a-z0-9]+/);
      for (var w = 0; w < words.length; w++) {
        var n = 0;
        while (n < tok.length && n < words[w].length && tok.charAt(n) === words[w].charAt(n)) n++;
        if (n >= 4 && n >= tok.length - 2) return 30;
      }
    }
    if (tok.length < 3) return 0;
    var at = 0, gaps = 0;
    for (var k = 0; k < tok.length; k++) {
      var j = t.indexOf(tok.charAt(k), at);
      if (j < 0) return 0;
      if (k && j > at) gaps += j - at;
      at = j + 1;
    }
    return Math.max(1, 20 - gaps);
  }
  // match scores the query against fields ([text, weight] pairs): every
  // token must match some field; the best field for each token counts. With
  // strict, a token must appear as written (no loose letters-in-order match).
  function match(tokens, fields, strict) {
    if (!tokens.length) return 1;
    var total = 0;
    for (var i = 0; i < tokens.length; i++) {
      var best = 0;
      for (var f = 0; f < fields.length; f++) {
        var raw = score(fields[f][0], tokens[i]);
        if (strict && raw < 30) raw = 0;
        var s = raw * fields[f][1];
        if (s > best) best = s;
      }
      if (!best) return 0;
      total += best;
    }
    return total;
  }

  function tokensOf(q) {
    return q.toLowerCase().split(/\s+/).filter(Boolean);
  }

  // ---- The page -----------------------------------------------------------
  function currentSlug() {
    var p = location.pathname.split('/');
    if (p.length < 2 || !p[1] || p[1] === '-' || p[1] === '_static') return '';
    return decodeURIComponent(p[1]);
  }
  function currentPart() {
    var p = location.pathname.split('/');
    return p.length > 2 ? decodeURIComponent(p[2] || '') : '';
  }
  // The agent panel on a reading page: the "Ask" action.
  function agentAsk() {
    var panel = window.latheAgentPanel;
    var composer = document.getElementById('askInput');
    if (!panel || !composer || !document.getElementById('askForm')) return null;
    var toggle = document.getElementById('agentToggle');
    var nameEl = document.getElementById('agentToggleName');
    var name = nameEl ? nameEl.textContent.trim() : '';
    if (!name || name === 'Agent' || (toggle && toggle.getAttribute('data-state') === 'none')) name = 'your agent';
    return {name: name, panel: panel, composer: composer};
  }

  var statusWords = {verified: 'Verified', verifying: 'Verifying', failed: 'Needs a look', skipped: 'Skipped', extending: 'Being written', unverified: 'Not verified'};

  // ---- Results ------------------------------------------------------------
  function tutorialResults(tokens, slug) {
    if (!index) return [];
    var out = [];
    index.tutorials.forEach(function (t, order) {
      var fields = [[t.title, 3], [t.slug, 1.5], [t.topic, 1.5], [t.shelf && t.shelf.name, 1]];
      (t.tags || []).forEach(function (g) { fields.push([g, 1.5]); });
      (t.tools || []).forEach(function (x) { fields.push([x.name + (x.version ? ' ' + x.version : ''), 1]); });
      var s = match(tokens, fields);
      if (!s) return;
      var meta = [];
      if (t.shelf && t.shelf.name) meta.push(t.shelf.name);
      meta.push(statusWords[t.status] || 'Not verified');
      if (t.parts.length > 1) meta.push(t.parts.length + ' parts');
      out.push({
        kind: 'tutorial', label: t.title, meta: meta.join(' · '), href: t.href,
        cover: t.cover, status: t.status, hl: tokens,
        sort: s + (t.slug === slug ? 0.5 : 0) - order / 1000
      });
    });
    return out;
  }

  function sectionResults(tokens, slug) {
    if (!index) return [];
    var out = [];
    var part = currentPart();
    index.tutorials.forEach(function (t) {
      var here = t.slug === slug;
      // With no query, list only the tutorial being read.
      if (!tokens.length && !here) return;
      var series = t.parts.length > 1;
      t.parts.forEach(function (p) {
        var marker = p.number ? 'P' + p.number : '';
        if (series) {
          var ps = match(tokens, [[p.title, 2.5], [marker.toLowerCase(), 1], [t.title, 0.6]]);
          if (ps) {
            out.push({
              kind: 'part', marker: marker, label: p.title, meta: t.title, href: p.href, hl: tokens,
              // With no query, the parts in order, ahead of the sections.
              sort: tokens.length ? ps + (here ? 30 : 0) + (here && p.part === part ? 5 : 0) - p.number / 100 : 100 - p.number
            });
          }
        }
        // With no query, the sections of the part being read.
        if (!tokens.length && p.part !== part && !(part === '' && !p.number)) return;
        p.headings.forEach(function (h, hi) {
          var hs = match(tokens, [[h.text, 2.5], [p.title, 0.5], [t.title, 0.5]]);
          if (!hs) return;
          out.push({
            kind: 'section', marker: '§', level: h.level, label: h.text,
            meta: series ? (here ? '' : t.title + ' · ') + marker + ' ' + p.title : t.title,
            href: p.href + '#' + h.id, hl: tokens,
            sort: hs + (here ? 30 : 0) + (here && p.part === part ? 10 : 0) - hi / 1000
          });
        });
      });
    });
    return out;
  }

  function commandResults(tokens) {
    var sc = window.latheShortcuts;
    if (!sc) return [];
    var out = [];
    sc.list.forEach(function (s, i) {
      if (s.id === 'library.goto' || !sc.available(s.id)) return;
      var label = (!mac && s.otherLabel) ? s.otherLabel : s.label;
      if (s.submenu) label = s.submenu + ': ' + label;
      var m = match(tokens, [[label, 2], [s.group, 0.5], [s.menu, 0.5]], true);
      if (!m) return;
      out.push({kind: 'command', label: label.replace(/…$/, ''), meta: s.group, key: displayCombo(mac ? s.mac : s.other), command: s.id, hl: tokens, sort: m - i / 1000});
    });
    return out;
  }

  // The actions built from the query itself.
  function queryActions(q, tokens) {
    var out = [];
    if (!q) return out;
    out.push({kind: 'new', label: 'New tutorial about “' + q + '”…', command: '/lathe ' + q});
    if (index) {
      // The two most used tags that match.
      index.tags.filter(function (g) { return match(tokens, [[g.tag, 1]], true); }).slice(0, 2).forEach(function (g) {
        out.push({kind: 'tag', label: 'Show tutorials tagged ' + g.tag, count: g.count, href: g.href});
      });
    }
    var ask = agentAsk();
    if (ask) {
      out.push({kind: 'ask', label: 'Ask ' + ask.name + ' about “' + q + '” in this part', key: displayCombo(mac ? 'Cmd+L' : 'Ctrl+L'), query: q});
    }
    return out;
  }

  function byScore(a, b) { return b.sort - a.sort; }

  function results() {
    var raw = input.value;
    var actionsOnly = /^\s*>/.test(raw);
    var q = raw.replace(/^\s*>/, '').trim();
    var tokens = tokensOf(q);
    var slug = currentSlug();
    modeEl.hidden = !actionsOnly;
    var groups = [];
    if (!actionsOnly && tokens.length) {
      var tuts = tutorialResults(tokens, slug).sort(byScore).slice(0, 4);
      if (tuts.length) groups.push({label: 'Tutorials', items: tuts});
      var secs = sectionResults(tokens, slug).sort(byScore).slice(0, 6);
      if (secs.length) groups.push({label: 'Parts and sections', items: secs});
    } else if (!actionsOnly) {
      // Nothing typed: on a reading page, this tutorial's parts and this
      // part's sections first, then the newest tutorials.
      var here = sectionResults(tokens, slug).sort(byScore).slice(0, 12);
      if (here.length) groups.push({label: 'In this tutorial', items: here});
      var recent = tutorialResults(tokens, '').filter(function (it) { return it.href !== '/' + slug + '/'; }).slice(0, here.length ? 4 : 6);
      if (recent.length) groups.push({label: 'Recent tutorials', items: recent});
    }
    // Commands that match by name first: ">dark" is the Dark command.
    var acts = commandResults(tokens).sort(byScore).concat(queryActions(q, tokens));
    if (!actionsOnly && tokens.length) acts = acts.slice(0, 6);
    if (acts.length) groups.push({label: 'Actions', items: acts});
    return {groups: groups, q: q, actionsOnly: actionsOnly};
  }

  // ---- Rendering ----------------------------------------------------------
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }
  // highlight appends text with the first match of each token marked.
  function highlight(node, text, tokens) {
    var lower = text.toLowerCase(), marks = [];
    (tokens || []).forEach(function (tok) {
      var i = lower.indexOf(tok);
      if (i >= 0) marks.push([i, i + tok.length]);
    });
    marks.sort(function (a, b) { return a[0] - b[0]; });
    var at = 0;
    marks.forEach(function (m) {
      if (m[0] < at) return;
      if (m[0] > at) node.appendChild(document.createTextNode(text.slice(at, m[0])));
      node.appendChild(el('mark', '', text.slice(m[0], m[1])));
      at = m[1];
    });
    if (at < text.length) node.appendChild(document.createTextNode(text.slice(at)));
  }

  function optionNode(it, id) {
    var o = el('div', 'palette-option palette-' + it.kind);
    o.id = id;
    o.setAttribute('role', 'option');
    o.setAttribute('aria-selected', 'false');
    var lead = el('span', 'palette-lead');
    lead.setAttribute('aria-hidden', 'true');
    if (it.kind === 'tutorial') {
      lead.className += ' palette-swatch';
      lead.setAttribute('data-cover', String(it.cover || 1));
    } else if (it.marker) {
      lead.textContent = it.marker;
      if (it.kind === 'section' && it.level === 3) o.className += ' palette-h3';
    } else {
      lead.textContent = it.kind === 'new' ? '+' : it.kind === 'ask' ? '?' : it.kind === 'tag' ? '#' : '›';
    }
    o.appendChild(lead);
    var body = el('span', 'palette-body');
    var label = el('span', 'palette-label');
    highlight(label, it.label, it.hl);
    if (it.kind === 'tag') label.appendChild(el('span', 'palette-count', ' · ' + it.count));
    body.appendChild(label);
    if (it.kind === 'new') {
      var cmd = el('span', 'palette-meta');
      cmd.appendChild(el('code', 'palette-cmd', it.command));
      cmd.appendChild(document.createTextNode(' Copies the command to run in your coding agent'));
      body.appendChild(cmd);
    } else if (it.meta) {
      var meta = el('span', 'palette-meta');
      if (it.kind === 'tutorial' && it.status && it.status !== 'unverified') {
        var dot = el('span', 'palette-dot');
        dot.setAttribute('data-status', it.status);
        meta.appendChild(dot);
      }
      meta.appendChild(document.createTextNode(it.meta));
      body.appendChild(meta);
    }
    o.appendChild(body);
    if (it.key) {
      var k = el('kbd', 'palette-key', it.key);
      k.setAttribute('aria-hidden', 'true');
      o.appendChild(k);
    }
    return o;
  }

  function render() {
    var res = results();
    listEl.textContent = '';
    items = [];
    var n = 0;
    res.groups.forEach(function (g, gi) {
      var group = el('div', 'palette-group');
      group.setAttribute('role', 'group');
      var head = el('div', 'palette-group-label', g.label);
      head.id = 'palette-group-' + gi;
      head.setAttribute('role', 'presentation');
      group.setAttribute('aria-labelledby', head.id);
      group.appendChild(head);
      g.items.forEach(function (it) {
        var id = 'palette-opt-' + (n++);
        var node = optionNode(it, id);
        var at = items.length;
        node.addEventListener('mousemove', function () { if (active !== at) setActive(at, false); });
        node.addEventListener('mousedown', function (ev) { ev.preventDefault(); });
        node.addEventListener('click', function (ev) { setActive(at, false); choose(ev.metaKey || ev.ctrlKey || ev.shiftKey); });
        items.push({data: it, node: node});
        group.appendChild(node);
      });
      listEl.appendChild(group);
    });
    if (!items.length) {
      var empty = el('p', 'palette-empty');
      if (!index && loading) empty.textContent = 'Loading your library…';
      else if (loadFailed && !index) empty.textContent = 'Could not reach Lathe to search the library.';
      else if (res.actionsOnly) empty.textContent = 'No actions match “' + res.q + '”.';
      else empty.textContent = res.q ? 'Nothing matches “' + res.q + '”.' : 'Your library is empty.';
      listEl.appendChild(empty);
    }
    setActive(items.length ? 0 : -1, true);
    clearTimeout(statusTimer);
    statusTimer = setTimeout(function () {
      statusEl.textContent = items.length ? items.length + ' result' + (items.length === 1 ? '' : 's') : 'No results';
    }, 400);
  }

  function setActive(i, scroll) {
    if (active >= 0 && items[active]) items[active].node.setAttribute('aria-selected', 'false');
    active = i;
    if (i < 0 || !items[i]) { input.removeAttribute('aria-activedescendant'); return; }
    var node = items[i].node;
    node.setAttribute('aria-selected', 'true');
    input.setAttribute('aria-activedescendant', node.id);
    if (scroll !== false) {
      if (i === 0) listEl.scrollTop = 0;
      else node.scrollIntoView({block: 'nearest'});
    }
  }

  function move(d) {
    if (!items.length) return;
    setActive((active + d + items.length) % items.length, true);
  }

  // ---- Opening things -----------------------------------------------------
  // In a new window: palette.html defines the opener (an app window in the
  // desktop app, a tab in a browser).
  function openWindow(href) {
    var opener = window.lathePaletteOpenWindow;
    if (!opener) return;
    opener(href).catch(function () {
      if (window.latheAnnounce) window.latheAnnounce('Could not open a separate window');
    });
  }
  function navigate(href) {
    var u = new URL(href, location.href);
    if (u.pathname === location.pathname && u.search === location.search && u.hash) {
      // A section of this page: jump without a reload.
      close(false);
      location.hash = u.hash;
      var target = document.getElementById(decodeURIComponent(u.hash.slice(1)));
      if (target) {
        if (!target.hasAttribute('tabindex')) target.setAttribute('tabindex', '-1');
        try { target.focus({preventScroll: true}); } catch (e) {}
      }
      return;
    }
    close(false);
    location.href = href;
  }

  function copy(text) {
    function fallback() {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      dlg.appendChild(ta);
      ta.select();
      var ok = false;
      try { ok = document.execCommand('copy'); } catch (e) {}
      dlg.removeChild(ta);
      return ok ? Promise.resolve() : Promise.reject(new Error('copy'));
    }
    if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(text).catch(fallback);
    return fallback();
  }

  function choose(newWindow) {
    var it = items[active] && items[active].data;
    if (!it) return;
    switch (it.kind) {
      case 'tutorial': case 'part': case 'section': case 'tag':
        if (newWindow) { openWindow(it.href); close(true); } else navigate(it.href);
        return;
      case 'command':
        close(true);
        window.latheShortcuts.run(it.command);
        return;
      case 'ask':
        var ask = agentAsk();
        close(false);
        if (!ask) return;
        ask.panel.open({focus: 'ask', session: true});
        ask.composer.value = 'About “' + it.query + '” in this part: ';
        ask.composer.dispatchEvent(new Event('input', {bubbles: true}));
        try {
          ask.composer.focus();
          ask.composer.setSelectionRange(ask.composer.value.length, ask.composer.value.length);
        } catch (e) {}
        return;
      case 'new':
        // Starting a tutorial from the app isn't built yet (#138): hand over
        // the /lathe command to run in the coding agent.
        var node = items[active].node;
        copy(it.command).then(function () {
          statusEl.textContent = 'Copied ' + it.command + '. Run it in your coding agent.';
          var meta = node.querySelector('.palette-meta');
          if (meta) {
            meta.textContent = '';
            meta.appendChild(el('code', 'palette-cmd', it.command));
            meta.appendChild(document.createTextNode(' Copied. Run it in your coding agent'));
          }
        }, function () {
          statusEl.textContent = 'Could not copy. Run ' + it.command + ' in your coding agent.';
        });
        return;
    }
  }

  // ---- Open and close -----------------------------------------------------
  function open() {
    if (dlg.open) { input.focus(); input.select(); return; }
    returnFocus = document.activeElement;
    input.value = '';
    dlg.showModal();
    root.setAttribute('data-palette-open', '');
    render();
    input.focus();
    load();
  }
  function close(restore) {
    if (!dlg.open) return;
    dlg.close();
    root.removeAttribute('data-palette-open');
    statusEl.textContent = '';
    if (restore !== false && returnFocus && typeof returnFocus.focus === 'function' && document.contains(returnFocus)) {
      try { returnFocus.focus({preventScroll: true}); } catch (e) {}
    }
    returnFocus = null;
  }

  input.addEventListener('input', render);
  input.addEventListener('keydown', function (ev) {
    if (ev.isComposing) return;
    switch (ev.key) {
      case 'ArrowDown': ev.preventDefault(); move(1); return;
      case 'ArrowUp': ev.preventDefault(); move(-1); return;
      case 'Home': if (ev.ctrlKey) { ev.preventDefault(); setActive(items.length ? 0 : -1, true); } return;
      case 'End': if (ev.ctrlKey) { ev.preventDefault(); setActive(items.length - 1, true); } return;
      case 'Enter':
        ev.preventDefault();
        choose(ev.metaKey || ev.ctrlKey);
        return;
    }
  });
  // Keys inside the palette are its own: the page's shortcuts (shortcuts.js,
  // the agent panel) don't run under it. ⌘K / Ctrl+K closes it again.
  dlg.addEventListener('keydown', function (ev) {
    if ((ev.key === 'k' || ev.key === 'K') && (mac ? ev.metaKey : ev.ctrlKey) && !ev.altKey && !ev.shiftKey) {
      ev.preventDefault();
      close(true);
    }
    ev.stopPropagation();
  });
  dlg.addEventListener('cancel', function (ev) { ev.preventDefault(); close(true); });
  // A click on the backdrop (the dialog's own box, outside its content).
  dlg.addEventListener('click', function (ev) {
    if (ev.target !== dlg) return;
    var r = dlg.getBoundingClientRect();
    if (ev.clientX < r.left || ev.clientX > r.right || ev.clientY < r.top || ev.clientY > r.bottom) close(true);
  });
  if (closeBtn) closeBtn.addEventListener('click', function () { close(true); });

  // The masthead search buttons: links to All tutorials with scripts off.
  document.querySelectorAll('[data-palette-open]').forEach(function (b) {
    b.setAttribute('role', 'button');
    // The key hint shows only once the key works.
    b.setAttribute('data-palette-ready', '');
    b.setAttribute('aria-haspopup', 'dialog');
    b.addEventListener('click', function (ev) {
      if (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.button) return;
      ev.preventDefault();
      open();
    });
    b.addEventListener('keydown', function (ev) {
      if (ev.key === ' ') { ev.preventDefault(); open(); }
    });
    // Warm the index before the first open.
    b.addEventListener('pointerenter', function () { if (!index) load(); }, {once: true});
  });

  window.lathePalette = {open: open, close: close, isOpen: function () { return dlg.open; }};
  if (window.latheShortcuts && window.latheShortcuts.refresh) window.latheShortcuts.refresh();
})();
