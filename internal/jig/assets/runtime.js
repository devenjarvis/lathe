// Lathe jig runtime. Inlined on a reading page only when the part contains a
// jig (internal/jig.Script). Each kind's enhancer registers a mount function;
// mountAll runs it on every server-rendered frame (<figure data-jig="<kind>">).
//
// The contract: the server HTML already teaches with scripts off, so a mount
// only adds interaction. A kind with no enhancer, or one that throws, leaves the
// fallback in place.
(function(){
  'use strict';
  var kinds = {};
  var api = {
    // el('button', {type:'button', class:'btn'}, [child, 'text']) builds an
    // element. Attribute values are set with setAttribute; children may be
    // nodes or strings (strings become text nodes, never HTML).
    el: function(tag, attrs, children){
      var node = document.createElement(tag);
      Object.keys(attrs || {}).forEach(function(k){ node.setAttribute(k, attrs[k]); });
      (children || []).forEach(function(c){
        node.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
      });
      return node;
    },
    // data(frame) returns the JSON the Go side wrote with jig.WriteData, or null.
    data: function(frame){
      var s = frame.querySelector('script.jig-data[type="application/json"]');
      if (!s) return null;
      try { return JSON.parse(s.textContent); } catch (e) { return null; }
    }
  };
  window.LatheJig = {
    register: function(name, mount){ kinds[name] = mount; },
    mountAll: function(root){
      var frames = (root || document).querySelectorAll('[data-jig]');
      Array.prototype.forEach.call(frames, function(frame){
        var name = frame.getAttribute('data-jig');
        var mount = kinds[name];
        if (!mount || frame.hasAttribute('data-jig-ready')) return;
        try {
          mount(frame, api);
          frame.setAttribute('data-jig-ready', '');
        } catch (err) {
          console.warn('jig ' + name + ' failed to mount', err);
        }
      });
    }
  };
})();
