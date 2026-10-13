/* lupa.js — счётчик. Без cookie, ~3 КБ. Подключение:
   <script async src="https://stats.example.ru/lupa.js" data-site="1" data-clicks data-replay="10"></script>
   data-clicks включает карту кликов, data-replay="N" записывает N % визитов для вебвизора.
   События: lupa('event_name', {key: 'value'}) */
(function () {
  if (window.__lupa) return; window.__lupa = 1;
  var s = document.currentScript || (function () { var a = document.getElementsByTagName('script'); return a[a.length - 1]; })();
  var site = s && s.getAttribute('data-site');
  if (!site) return;
  var base = (s.getAttribute('data-api') || s.src.replace(/\/lupa\.js(\?.*)?$/, ''));
  var api = base + '/api/event';
  // карта кликов (data-clicks) и вебвизор (data-replay="доля визитов в %") живут в отдельном файле
  window.__lupaCfg = { api: base, site: +site, clicks: s.hasAttribute('data-clicks'), replay: +(s.getAttribute('data-replay') || 0), maskText: s.hasAttribute('data-mask-text') };
  if (window.__lupaCfg.clicks || window.__lupaCfg.replay || /lupa-heatmap=/.test(location.hash)) {
    var v = document.createElement('script'); v.async = true; v.src = base + '/lupa-visual.js'; document.head.appendChild(v);
  }
  var start = Date.now(), maxScroll = 0, last = '';

  function send(name, extra) {
    var p = { s: +site, n: name, u: location.href, r: document.referrer, t: document.title,
              w: screen.width, h: screen.height, l: navigator.language };
    if (extra) for (var k in extra) p[k] = extra[k];
    var body = JSON.stringify(p);
    try {
      if (navigator.sendBeacon && navigator.sendBeacon(api, new Blob([body], { type: 'text/plain' }))) return;
    } catch (e) {}
    try { var x = new XMLHttpRequest(); x.open('POST', api, true); x.setRequestHeader('Content-Type', 'text/plain'); x.send(body); } catch (e) {}
  }
  function scrollPct() {
    var d = document.documentElement, b = document.body;
    var h = Math.max(d.scrollHeight, b ? b.scrollHeight : 0) - window.innerHeight;
    return h <= 0 ? 100 : Math.min(100, Math.round((window.scrollY || d.scrollTop) / h * 100));
  }
  function engage() {
    if (!last) return;
    send('engage', { d: Date.now() - start, sc: Math.max(maxScroll, scrollPct()) });
  }
  // Смена только #якоря просмотром не считается.
  function pageKey() { return location.pathname + location.search; }
  function pageview() {
    if (pageKey() === last) return;
    if (last) engage();
    last = pageKey(); start = Date.now(); maxScroll = 0;
    send('pageview');
  }
  window.addEventListener('scroll', function () { var p = scrollPct(); if (p > maxScroll) maxScroll = p; }, { passive: true });
  document.addEventListener('visibilitychange', function () { if (document.visibilityState === 'hidden') engage(); });
  window.addEventListener('pagehide', engage);
  // SPA: перехватываем pushState и popstate
  var ps = history.pushState;
  if (ps) history.pushState = function () { ps.apply(this, arguments); setTimeout(pageview, 0); };
  window.addEventListener('popstate', function () { setTimeout(pageview, 0); });

  var q = window.lupa && window.lupa.q;
  window.lupa = function (name, props) { if (name) send(String(name), { p: props || null }); };
  if (q) for (var i = 0; i < q.length; i++) window.lupa.apply(null, q[i]);
  pageview();
})();
