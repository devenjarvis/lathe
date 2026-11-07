// Lathe's keyboard shortcuts and menu commands on the page. The registry is
// internal/shortcuts (Go), inlined as #latheShortcuts; this file only maps a
// command id to what it does on this page.
//
// In a plain browser this binds the in-page keys. In the desktop app
// (data-native on <html>) the menu bar owns the combos, so the page binds none
// of the menu's keys (a key would otherwise run twice). Instead it opens
// GET /-/native/commands and runs each command the shell sends, and tells
// the shell what this page can run (POST /-/native/context) so menu items
// enable and disable with the page.
(function () {
  var el = document.getElementById('latheShortcuts');
  if (!el) return;
  var data;
  try { data = JSON.parse(el.textContent); } catch (e) { return; }
  var list = data.shortcuts || [];
  var root = document.documentElement;
  var native = root.hasAttribute('data-native');
  var mac = /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent || '');
  var popout = /[?&]agent=popout(&|$)/.test(location.search);

  // The same rule as shortcuts.ScopeForPath.
  function scopeFor(path) {
    if (!path || path === '/') return 'home';
    if (path.indexOf('/-/') === 0 || path.indexOf('/_static/') === 0) return 'app';
    return 'tutorial';
  }
  var scope = scopeFor(location.pathname);

  function $(sel) { return document.querySelector(sel); }
  function go(href) { return function () { location.href = href; }; }
  function clickable(sel) { var n = $(sel); return n && !n.disabled ? n : null; }

  function setTheme(want) {
    return function () {
      var cur = root.getAttribute('data-theme') || 'light';
      if (cur === want) return;
      var t = $('[data-theme-toggle]');
      if (t) t.click();
    };
  }

  // What each page-run command does here, and when this page can run it.
  // Commands the registry lists but that are not here (stubs, shell
  // commands) are not bound.
  // Text size: app only (a browser keeps its own zoom on these keys).
  function textStep(n) {
    return {
      when: function () { return native && !!window.lathePrefs; },
      run: function () { window.lathePrefs.step(n); }
    };
  }
  // An agent-panel action (check, verify, extend): the panel's action menu
  // decides whether it can run (e.g. not while another job runs). Without a
  // panel (no agent panel on the page) fall back to the page's own form.
  function panelAction(name, fallbackWhen, fallbackRun) {
    return {
      when: function () {
        var p = window.latheAgentPanel;
        if (p && p.canRun) return p.canRun(name);
        return fallbackWhen();
      },
      run: function () {
        var p = window.latheAgentPanel;
        if (p && p.runAction) { p.runAction(name); return; }
        fallbackRun();
      }
    };
  }
  var actions = {
    'library.home': {run: go('/')},
    // Go to anything: the palette (palette.js, included with this script).
    'library.goto': {
      when: function () { return !popout && !!window.lathePalette; },
      run: function () { window.lathePalette.open(); }
    },
    // New tutorial: the sheet (newsheet.js, included with this script).
    'library.new': {
      when: function () { return !popout && !!window.latheNewSheet; },
      run: function () { window.latheNewSheet.open({}); }
    },
    // The library page (list.html) says which cover is current.
    'library.trash': {
      when: function () { return !!window.latheLibrary && window.latheLibrary.target(); },
      run: function () { window.latheLibrary.trashTarget(); }
    },
    'reading.bigger': textStep(1),
    'reading.smaller': textStep(-1),
    'reading.actualsize': textStep(0),
    'reading.appearance.light': {when: function () { return !!$('[data-theme-toggle]'); }, run: setTheme('light')},
    'reading.appearance.dark': {when: function () { return !!$('[data-theme-toggle]'); }, run: setTheme('dark')},
    'reading.next': {
      when: function () { return !popout && !!$('.part-nav a.next'); },
      run: function () { $('.part-nav a.next').click(); }
    },
    'reading.prev': {
      when: function () { return !popout && !!$('.part-nav a.prev'); },
      run: function () { $('.part-nav a.prev').click(); }
    },
    'tutorial.verify': panelAction('verify', function () { return !!$('#verifyForm'); }, function () {
      var f = $('#verifyForm');
      if (f && typeof f.requestSubmit === 'function') f.requestSubmit();
    }),
    'tutorial.extend': panelAction('extend', function () { return !!$('#extendForm'); }, function () {
      var f = $('#extendForm');
      if (!f) return;
      f.scrollIntoView({block: 'center'});
      var ta = f.querySelector('textarea');
      if (ta) { try { ta.focus({preventScroll: true}); } catch (e) {} }
    }),
    'agent.panel': {
      when: function () { return !!window.latheAgentPanel; },
      run: function () {
        window.latheAgentPanel.open();
        var input = document.getElementById('askInput');
        if (input && input.offsetParent !== null) { try { input.focus(); } catch (e) {} }
      }
    },
    'agent.check': panelAction('check', function () {
      return !!$('#checkWork') && typeof window.latheCheckWork === 'function';
    }, function () { window.latheCheckWork(); }),
    'agent.popout': {
      when: function () { return !popout && !!clickable('#agentPopout'); },
      run: function () { $('#agentPopout').click(); }
    }
  };

  var byId = {};
  list.forEach(function (s) { byId[s.id] = s; });

  function allowed(s) { return s.scope === 'global' || s.scope === scope; }

  // available reports whether this page can run command id now.
  function available(id) {
    var s = byId[id];
    if (!s || s.stub || !allowed(s)) return false;
    if (s.handler === 'page') {
      var a = actions[id];
      return !!a && (!a.when || !!a.when());
    }
    // A shell command with a page to go to (Settings) works in a browser
    // as a navigation; in the app the shell handles it.
    return s.handler === 'shell' && !!s.href && !native;
  }

  function run(id) {
    if (!available(id)) return false;
    var s = byId[id];
    if (s.handler === 'shell') { location.href = s.href; return true; }
    actions[id].run();
    return true;
  }

  // Key matching: the registry writes combos as "Cmd+Shift+K" (macOS) and
  // "Ctrl+Shift+K" (Windows, Linux).
  var codes = {'[': 'BracketLeft', ']': 'BracketRight', ',': 'Comma', '=': 'Equal', '-': 'Minus', '/': 'Slash', '.': 'Period', ';': 'Semicolon', '`': 'Backquote'};
  function parse(combo) {
    var i = combo.lastIndexOf('+', combo.length - 2);
    var mods = i < 0 ? [] : combo.slice(0, i).split('+');
    var key = i < 0 ? combo : combo.slice(i + 1);
    return {
      key: key,
      meta: mods.indexOf('Cmd') >= 0,
      ctrl: mods.indexOf('Ctrl') >= 0,
      alt: mods.indexOf('Option') >= 0 || mods.indexOf('Alt') >= 0,
      shift: mods.indexOf('Shift') >= 0
    };
  }
  function keyMatches(k, ev) {
    if (k.key.length === 1) {
      var c = k.key.toUpperCase();
      if (/[A-Z]/.test(c)) return ev.code === 'Key' + c || (ev.key || '').toUpperCase() === c;
      if (/[0-9]/.test(c)) return ev.code === 'Digit' + c || ev.key === c;
      return ev.key === k.key || (codes[k.key] && ev.code === codes[k.key]);
    }
    return ev.key === k.key;
  }
  function matches(k, ev) {
    return ev.metaKey === k.meta && ev.ctrlKey === k.ctrl && ev.altKey === k.alt &&
      ev.shiftKey === k.shift && keyMatches(k, ev);
  }

  // Bound keys: built commands with a combo on this platform. In the app,
  // the menu carries every combo of a menu item, so the page binds none.
  var bound = [];
  list.forEach(function (s) {
    var combo = mac ? s.mac : s.other;
    if (!combo || s.stub || s.handler === 'native') return;
    if (native && s.menu) return;
    bound.push({id: s.id, k: parse(combo)});
  });

  if (bound.length) {
    document.addEventListener('keydown', function (ev) {
      if (ev.defaultPrevented || ev.isComposing) return;
      for (var i = 0; i < bound.length; i++) {
        if (matches(bound[i].k, ev)) {
          if (run(bound[i].id)) ev.preventDefault();
          return;
        }
      }
    });
  }

  // For the palette and anything else that runs commands by id.
  window.latheShortcuts = {list: list, groups: data.groups || [], scope: scope, available: available, run: run};

  if (!native || typeof EventSource !== 'function') return;

  // Desktop app: the menu bar's commands arrive over SSE.
  var client = '';
  function report() {
    if (!client) return;
    var enabled = list.filter(function (s) { return s.handler === 'page' && available(s.id); })
      .map(function (s) { return s.id; });
    fetch('/-/native/context', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({client: client, enabled: enabled})
    }).catch(function () {});
  }
  var es = new EventSource('/-/native/commands?path=' + encodeURIComponent(location.pathname + location.search));
  es.addEventListener('hello', function (ev) {
    try { client = JSON.parse(ev.data).client || ''; } catch (e) { client = ''; }
    report();
  });
  es.addEventListener('command', function (ev) {
    var id = '';
    try { id = JSON.parse(ev.data).id || ''; } catch (e) {}
    if (id) run(id);
    // A command can change what the page can run next (a check started).
    setTimeout(report, 0);
  });
  window.addEventListener('focus', report);
  // Page state that menu items depend on changes as the reader works (the
  // agent panel appears, a part is added); refresh when the reader comes
  // back to the window and after a status swap.
  document.addEventListener('visibilitychange', function () { if (!document.hidden) report(); });
  window.latheShortcuts.refresh = report;
})();
