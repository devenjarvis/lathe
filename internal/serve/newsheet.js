// The New tutorial sheet (#138). newsheet.html's empty <dialog> drops from
// the title bar; open() loads the form (GET /-/new?partial=1, the same
// "newSheetForm" partial the /-/new page renders) into it. The ways in:
// library.new (⌘N / Ctrl+N, the File menu; shortcuts.js), any
// [data-new-open] link (the title-bar button, the blank cover on Home) and
// ⌘K's "New tutorial about “…”" (palette.js, with the topic prefilled).
// With scripts off those are links to /-/new, whose form posts as-is.
//
// The form submits to POST /-/new as JSON. A hosted-agent start goes to the
// tutorial's page (/{slug}/, the being-written page) after a
// "lathe:new-started" event; a /lathe-work job or the handoff command is
// shown in the sheet.
(function () {
  var root = document.documentElement;
  var dlg = document.getElementById('newSheet');
  var slot = document.getElementById('newSheetSlot');
  var mac = /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent || '');
  var popout = /[?&]agent=popout(&|$)/.test(location.search);
  var pageForm = document.body && document.body.classList.contains('new-page') ? document.getElementById('newSheetForm') : null;
  var returnFocus = null;
  var loading = null;

  function $(form, sel) { return form.querySelector(sel); }
  function capital(s) { s = (s || '').trim(); return s.charAt(0).toUpperCase() + s.slice(1); }

  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(text);
    return Promise.reject(new Error('copy'));
  }

  function postJSON(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: {'Content-Type': 'application/json', 'Accept': 'application/json'},
      body: JSON.stringify(body)
    });
  }

  // wire runs one sheet form (in the dialog or on the /-/new page).
  function wire(form) {
    if (form.hasAttribute('data-wired')) return;
    form.setAttribute('data-wired', '');
    var topic = $(form, '#newTopic');
    var shelf = $(form, '#newShelf');
    var shelfHint = $(form, '#newShelfHint');
    var ws = $(form, '#newWorkspace');
    var wsHint = $(form, '#newWorkspaceHint');
    var versions = $(form, '#newVersions');
    var repo = $(form, '#newRepo');
    var branch = $(form, '#newRepoBranch');
    var detectedHint = $(form, '#newDetected');
    var summary = $(form, '#newMoreSummary');
    var errorEl = $(form, '#newError');
    var result = $(form, '#newResult');
    var submit = $(form, '#newSubmit');
    var autoWS = form.hasAttribute('data-auto-workspace');
    var touched = {};
    var timer = null, seq = 0;

    form.querySelectorAll('[data-new-kbd]').forEach(function (k) { k.textContent = mac ? '⌘↩' : 'Ctrl+↩'; });

    // The last level picked, when the server had none to show.
    var levels = form.querySelectorAll('input[name="level"]');
    if (!form.querySelector('input[name="level"]:checked')) {
      var last = null;
      try { last = localStorage.getItem('lathe-new-level'); } catch (e) {}
      levels.forEach(function (r) { if (r.value === last) r.checked = true; });
    }
    levels.forEach(function (r) {
      r.addEventListener('change', function () { try { localStorage.setItem('lathe-new-level', r.value); } catch (e) {} });
    });

    function setSummary() {
      if (!summary) return;
      var v = versions.value.trim(), r = repo.value.trim();
      summary.textContent = (v || r) ? [v, r].filter(Boolean).join(' · ') : 'The agent detects them and checks with you';
    }

    function apply(res) {
      if (!touched.shelf && shelf) {
        shelf.value = res.shelf || 'unsorted';
        if (shelfHint) shelfHint.textContent = res.shelfName ? 'Suggested: it matches what’s on ' + res.shelfName + '.' : 'No shelf matches yet, so it lands on Unsorted.';
      }
      if (autoWS && ws) ws.value = res.workspace;
      var d = res.detected || {};
      if (wsHint) {
        wsHint.textContent = !ws.value.trim() ? 'No workspace: the tutorial isn’t tied to a folder.'
          : d.exists ? 'This folder exists; the agent reads it but never changes it.'
          : 'Lathe makes the folder (and runs git init) when the agent starts. Empty for none.';
      }
      if (!touched.versions) versions.value = res.versions || '';
      if (!touched.repo) repo.value = d.repo || '';
      if (!touched.branch) branch.value = d.branch || '';
      if (detectedHint && !touched.versions && !touched.repo) {
        detectedHint.textContent = (d.tools && d.tools.length) || d.repo
          ? 'Found in the folder. Pinned versions are written against without asking.'
          : 'Pin versions to skip that question, e.g. zig 0.13.0, llvm 18.';
      }
      setSummary();
    }

    function suggest() {
      clearTimeout(timer);
      timer = null;
      var n = ++seq;
      var q = '/-/new/suggest?topic=' + encodeURIComponent(topic.value);
      if (!autoWS && ws.value.trim()) q += '&workspace=' + encodeURIComponent(ws.value.trim());
      return fetch(q, {headers: {'Accept': 'application/json'}}).then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      }).then(function (res) { if (n === seq) apply(res); }).catch(function () {});
    }
    function later() { clearTimeout(timer); timer = setTimeout(suggest, 250); }

    topic.addEventListener('input', later);
    // Enter in the topic starts writing (Shift+Enter is a new line).
    topic.addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter' && !ev.shiftKey && !ev.metaKey && !ev.ctrlKey && !ev.isComposing) {
        ev.preventDefault();
        send();
      }
    });
    form.querySelectorAll('[data-idea]').forEach(function (a) {
      a.addEventListener('click', function (ev) {
        ev.preventDefault();
        topic.value = a.getAttribute('data-idea');
        suggest();
        topic.focus();
      });
    });
    if (shelf) shelf.addEventListener('change', function () {
      touched.shelf = true;
      if (shelfHint) shelfHint.textContent = shelf.value === 'unsorted' ? 'It lands on Unsorted.' : 'You picked this shelf.';
    });
    ws.addEventListener('input', function () { autoWS = false; });
    ws.addEventListener('change', suggest);
    versions.addEventListener('input', function () { touched.versions = true; setSummary(); });
    repo.addEventListener('input', function () { touched.repo = true; setSummary(); });
    branch.addEventListener('input', function () { touched.branch = true; });

    // Choose folder… (desktop app only: the server renders the button with
    // a shell).
    var pick = $(form, '#newPickFolder');
    if (pick) pick.addEventListener('click', function () {
      pick.setAttribute('aria-busy', 'true');
      postJSON(pick.getAttribute('data-pick-url'), {'for': 'new-workspace', path: ws.value.trim()}).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
        return r.json();
      }).then(function (res) {
        if (!res.path) return;
        ws.value = res.display || res.path;
        autoWS = false;
        suggest();
      }).catch(function (err) {
        errorEl.textContent = capital(err.message);
      }).then(function () { pick.removeAttribute('aria-busy'); });
    });

    function wireCopy() {
      var btn = $(form, '#newResultCopy');
      var code = $(form, '#newResultCmd');
      if (!btn || !code || btn.hasAttribute('data-wired') || !(navigator.clipboard && navigator.clipboard.writeText)) return;
      btn.setAttribute('data-wired', '');
      btn.hidden = false;
      btn.addEventListener('click', function () {
        copyText(code.textContent).then(function () {
          btn.textContent = 'Copied';
          setTimeout(function () { btn.textContent = 'Copy'; }, 1600);
        }, function () {});
      });
    }
    wireCopy();

    function showResult(html, command) {
      var msg = result.querySelector('.new-result-msg');
      var cmdRow = result.querySelector('.new-result-cmd');
      msg.innerHTML = html;
      if (cmdRow) {
        cmdRow.hidden = !command;
        $(form, '#newResultCmd').textContent = command || '';
      }
      result.hidden = false;
      wireCopy();
      try { result.scrollIntoView({block: 'nearest'}); } catch (e) {}
    }
    function esc(s) { var d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

    function send() {
      if (submit.getAttribute('aria-busy') === 'true') return;
      // Typed faster than the live update: settle the shelf, folder and
      // versions first, so what is sent is what the sheet shows.
      if (timer) { suggest().then(send); return; }
      errorEl.textContent = '';
      result.hidden = true;
      if (!topic.value.trim()) {
        errorEl.textContent = 'Say what you want to build first.';
        topic.focus();
        return;
      }
      var checked = function (name) { var r = form.querySelector('input[name="' + name + '"]:checked'); return r ? r.value : ''; };
      var body = {
        topic: topic.value, level: checked('level'), voice: checked('voice'),
        workspace: ws.value.trim(), workspaceAuto: autoWS, versions: versions.value, repo: repo.value.trim(),
        repoBranch: branch.value.trim(), shelf: shelf ? shelf.value : ''
      };
      submit.setAttribute('aria-busy', 'true');
      submit.disabled = true;
      postJSON('/-/new', body).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
        return r.json();
      }).then(function (res) {
        if (res.mode === 'agent') {
          // #139's progress view can take over from here.
          var ev = new CustomEvent('lathe:new-started', {detail: {slug: res.slug, runId: res.runId}, cancelable: true});
          if (document.dispatchEvent(ev)) location.href = '/' + encodeURIComponent(res.slug) + '/';
          return;
        }
        if (res.mode === 'queued') {
          showResult('<strong>Sent to your /lathe-work session.</strong> It writes Part 1 there and the tutorial lands in your library once it’s stored.', '');
        } else {
          showResult((res.notice ? esc(res.notice) + ' ' : '') + '<strong>Run this in your coding agent</strong> to write Part 1 with these answers:', res.command);
        }
      }).catch(function (err) {
        errorEl.textContent = capital(String(err.message || err).replace(/^conflict:\s*/, ''));
      }).then(function () {
        submit.removeAttribute('aria-busy');
        submit.disabled = false;
      });
    }

    form.addEventListener('submit', function (ev) { ev.preventDefault(); send(); });
    form.addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter' && (mac ? ev.metaKey : ev.ctrlKey) && !ev.altKey && !ev.isComposing) {
        ev.preventDefault();
        send();
      }
    });
    form.querySelectorAll('[data-new-close]').forEach(function (b) {
      b.addEventListener('click', function () { close(true); });
    });
    // A server-rendered page may arrive with the topic filled in already.
    form.latheSuggest = suggest;
    form.latheTopic = topic;
  }

  function focusTopic(form, topic) {
    var t = form.latheTopic;
    if (!t) return;
    if (topic) { t.value = topic; form.latheSuggest(); }
    try { t.focus(); t.setSelectionRange(t.value.length, t.value.length); } catch (e) {}
  }

  function open(opts) {
    opts = opts || {};
    if (pageForm) { focusTopic(pageForm, opts.topic); return; }
    if (!dlg || popout) { location.href = '/-/new' + (opts.topic ? '?topic=' + encodeURIComponent(opts.topic) : ''); return; }
    var form = slot.querySelector('form');
    if (dlg.open) { if (form) focusTopic(form, opts.topic); return; }
    returnFocus = document.activeElement;
    if (window.lathePalette && window.lathePalette.isOpen()) window.lathePalette.close(false);
    dlg.showModal();
    root.setAttribute('data-new-open', '');
    // Keep a draft between openings, unless a topic is handed in or the
    // last one finished.
    var result = form && form.querySelector('#newResult');
    if (form && !opts.topic && result && result.hidden) { focusTopic(form, ''); return; }
    slot.innerHTML = '<p class="new-sheet-loading">Loading…</p>';
    var n = loading = {};
    fetch('/-/new?partial=1' + (opts.topic ? '&topic=' + encodeURIComponent(opts.topic) : ''), {headers: {'Accept': 'text/html'}})
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.text(); })
      .then(function (html) {
        if (n !== loading || !dlg.open) return;
        slot.innerHTML = html;
        var f = slot.querySelector('form');
        wire(f);
        focusTopic(f, '');
      })
      .catch(function () {
        // The page form works without the sheet.
        location.href = '/-/new' + (opts.topic ? '?topic=' + encodeURIComponent(opts.topic) : '');
      });
  }

  function close(restore) {
    if (!dlg || !dlg.open) return;
    dlg.close();
    root.removeAttribute('data-new-open');
    if (restore !== false && returnFocus && typeof returnFocus.focus === 'function' && document.contains(returnFocus)) {
      try { returnFocus.focus({preventScroll: true}); } catch (e) {}
    }
    returnFocus = null;
  }

  if (dlg) {
    dlg.addEventListener('cancel', function (ev) { ev.preventDefault(); close(true); });
    // Keys inside the sheet are its own (like the palette's): the page's
    // shortcuts don't run under it.
    dlg.addEventListener('keydown', function (ev) { ev.stopPropagation(); });
  }

  if (pageForm) wire(pageForm);

  document.querySelectorAll('[data-new-open]').forEach(function (b) {
    b.setAttribute('aria-haspopup', 'dialog');
    b.addEventListener('click', function (ev) {
      if (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.button) return;
      ev.preventDefault();
      open({});
    });
  });

  window.latheNewSheet = {open: open, close: close, isOpen: function () { return !!(dlg && dlg.open); }};

  // A page opened at #new (the first-launch setup's "Make your first
  // tutorial" lands on /#new) opens the sheet over itself.
  if (dlg && !pageForm && !popout && location.hash === '#new') {
    try { history.replaceState(null, '', location.pathname + location.search); } catch (e) {}
    open({});
  }
  if (window.latheShortcuts && window.latheShortcuts.refresh) window.latheShortcuts.refresh();
})();
