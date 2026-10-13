/* lupa-visual.js — дополнение к счётчику: карта кликов и вебвизор.
   Подключается самим lupa.js, если у тега есть data-clicks или data-replay. */
(function () {
  var L = window.__lupaCfg; if (!L) return;
  var api = L.api, site = L.site;

  function post(path, body) {
    var s = JSON.stringify(body);
    try { if (navigator.sendBeacon && navigator.sendBeacon(api + path, new Blob([s], { type: 'text/plain' }))) return; } catch (e) {}
    try { var x = new XMLHttpRequest(); x.open('POST', api + path, true); x.setRequestHeader('Content-Type', 'text/plain'); x.send(s); } catch (e) {}
  }

  // ---- карта кликов: селектор, текст, положение в долях ширины документа
  if (L.clicks) {
    var buf = [], timer = null;
    function selectorOf(el) {
      var parts = [], depth = 0;
      while (el && el.nodeType === 1 && depth < 5 && el !== document.body) {
        var s = el.tagName.toLowerCase();
        if (el.id) { parts.unshift(s + '#' + el.id); break; }
        var cls = (el.className && typeof el.className === 'string') ? el.className.trim().split(/\s+/).slice(0, 2).join('.') : '';
        if (cls) s += '.' + cls;
        var p = el.parentNode;
        if (p && !cls) { var same = Array.prototype.filter.call(p.children, function (c) { return c.tagName === el.tagName; }); if (same.length > 1) s += ':nth-of-type(' + (same.indexOf(el) + 1) + ')'; }
        parts.unshift(s); el = p; depth++;
      }
      return parts.join('>');
    }
    document.addEventListener('click', function (e) {
      var t = e.target; if (!t || t.nodeType !== 1) return;
      var a = t.closest ? (t.closest('a,button,[role=button],input,select,label,summary') || t) : t;
      var doc = document.documentElement, w = Math.max(doc.scrollWidth, doc.clientWidth) || 1;
      var txt = (a.innerText || a.value || a.getAttribute('aria-label') || '').trim().replace(/\s+/g, ' ').slice(0, 60);
      buf.push({ s: site, u: location.href, x: (e.pageX || 0) / w, y: Math.round(e.pageY || 0), vw: window.innerWidth, vh: window.innerHeight, sel: selectorOf(a), txt: txt });
      if (!timer) timer = setTimeout(flush, 1500);
    }, true);
    function flush() { timer = null; if (!buf.length) return; var b = buf; buf = []; post('/api/click', b); }
    window.addEventListener('pagehide', flush);
    document.addEventListener('visibilitychange', function () { if (document.visibilityState === 'hidden') flush(); });
  }

  // ---- наложение карты кликов: страница открыта из панели с #lupa-heatmap=<token>&p=<period>
  var m = /lupa-heatmap=([^&]+)(?:&p=([^&]*))?/.exec(location.hash);
  if (m) {
    var token = m[1], preset = m[2] || '7d';
    history.replaceState(null, '', location.pathname + location.search);
    var x = new XMLHttpRequest();
    x.open('GET', api + '/api/heatmap?s=' + site + '&path=' + encodeURIComponent(location.pathname) + '&preset=' + preset + '&token=' + token, true);
    x.onload = function () {
      if (x.status !== 200) { alert('Lupa: не удалось получить данные карты (' + x.status + ')'); return; }
      drawHeatmap(JSON.parse(x.responseText));
    };
    x.send();
  }
  function drawHeatmap(d) {
    var doc = document.documentElement, W = Math.max(doc.scrollWidth, doc.clientWidth), H = Math.max(doc.scrollHeight, doc.clientHeight);
    var c = document.createElement('canvas'); c.width = W; c.height = H;
    c.style.cssText = 'position:absolute;left:0;top:0;width:' + W + 'px;height:' + H + 'px;pointer-events:none;z-index:2147483646;opacity:.85';
    var ctx = c.getContext('2d'), pts = d.points || [], n = pts.length, r = Math.max(14, Math.min(40, Math.round(W / 60)));
    var inten = Math.max(0.08, Math.min(0.5, 25 / Math.max(n, 1)));
    pts.forEach(function (p) {
      var g = ctx.createRadialGradient(p.x * W, p.y, 0, p.x * W, p.y, r);
      g.addColorStop(0, 'rgba(255,0,0,' + inten + ')'); g.addColorStop(0.5, 'rgba(255,160,0,' + (inten * 0.5) + ')'); g.addColorStop(1, 'rgba(255,255,0,0)');
      ctx.fillStyle = g; ctx.beginPath(); ctx.arc(p.x * W, p.y, r, 0, Math.PI * 2); ctx.fill();
    });
    document.body.appendChild(c);
    var box = document.createElement('div');
    box.style.cssText = 'position:fixed;right:12px;top:12px;z-index:2147483647;background:#111;color:#fff;font:13px/1.4 -apple-system,Segoe UI,Roboto,sans-serif;padding:10px 12px;border-radius:8px;max-width:320px;box-shadow:0 4px 20px rgba(0,0,0,.3)';
    var els = (d.elements || []).slice(0, 8).map(function (e) { return '<div style="display:flex;gap:8px;justify-content:space-between"><span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + (e.text || e.selector).replace(/</g, '&lt;') + '</span><b>' + e.clicks + '</b></div>'; }).join('');
    box.innerHTML = '<b>Lupa · карта кликов</b> <span style="opacity:.7">' + n + ' кликов, ' + (d.pageviews || 0) + ' просмотров</span>' + els +
      '<div style="margin-top:6px;opacity:.7">Скролл: ' + (d.scroll || []).map(function (s) { return s.depth + '% → ' + Math.round(s.reached * 100) + '%'; }).join(' · ') + '</div>' +
      '<button id="lupa-hm-close" style="margin-top:8px;font:inherit;padding:4px 8px;border-radius:6px;border:0;background:#444;color:#fff;cursor:pointer">Закрыть</button>';
    document.body.appendChild(box);
    document.getElementById('lupa-hm-close').onclick = function () { c.remove(); box.remove(); };
  }

  // ---- вебвизор: запись через rrweb, чанки раз в 5 секунд
  if (L.replay > 0) {
    var rec = null;
    try { rec = sessionStorage.getItem('lupa_rec'); } catch (e) {}
    if (rec === null) { rec = Math.random() * 100 < L.replay ? '1' : '0'; try { sessionStorage.setItem('lupa_rec', rec); } catch (e) {} }
    if (rec === '1') {
      var rid = null;
      try { rid = sessionStorage.getItem('lupa_rid'); } catch (e) {}
      if (!rid) { rid = (Date.now().toString(36) + Math.random().toString(36).slice(2, 10)); try { sessionStorage.setItem('lupa_rid', rid); } catch (e) {} }
      var seq = +(function () { try { return sessionStorage.getItem('lupa_seq') || 0; } catch (e) { return 0; } })();
      var sc = document.createElement('script'); sc.async = true; sc.src = api + '/static/vendor/rrweb.min.js';
      sc.onload = function () {
        if (!window.rrweb) return;
        var events = [], total = 0, MAX = 5 * 1024 * 1024;
        function send() {
          if (!events.length) return;
          var chunk = events; events = [];
          var body = { s: site, rid: rid, seq: seq++, u: location.href, r: document.referrer, f: chunk[0].timestamp, l: chunk[chunk.length - 1].timestamp, e: chunk };
          try { sessionStorage.setItem('lupa_seq', seq); } catch (e) {}
          var str = JSON.stringify(body); total += str.length;
          if (total > MAX) { stop && stop(); return; }
          post('/api/replay', body);
        }
        var stop = window.rrweb.record({
          emit: function (ev) { events.push(ev); if (events.length >= 200) send(); },
          maskAllInputs: true,                 // содержимое полей не записывается никогда
          maskTextSelector: L.maskText ? '*' : '[data-lupa-mask]',
          blockSelector: '[data-lupa-block]',  // элементы с этим атрибутом заменяются заглушкой
          sampling: { mousemove: 50, scroll: 150, input: 'last' },
          checkoutEveryNms: 60000
        });
        var iv = setInterval(send, 5000);
        window.addEventListener('pagehide', send);
        document.addEventListener('visibilitychange', function () { if (document.visibilityState === 'hidden') send(); });
      };
      document.head.appendChild(sc);
    }
  }
})();
