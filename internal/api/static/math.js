// KaTeX costs about 300 KB of CSS+JS plus another 100-180 KB of woff2 fonts, and
// most pages here contain no math at all. So nothing is referenced from the page
// template: this script looks for the nodes the Markdown renderer emits
// (<span class="math inline">\( ... \)</span> / <span class="math display">) and
// only then pulls the library. A page without formulas never contacts the CDN.
//
// Rendering only those nodes also keeps KaTeX away from code blocks, where $ has
// no business being math. If the CDN is unreachable the raw TeX stays on screen,
// still readable, so nothing depends on this script succeeding.
(function () {
  var CDN = "https://cdn.jsdelivr.net/npm/katex@0.16.11/dist/";

  function mathNodes() {
    return document.querySelectorAll("span.math");
  }

  function render() {
    if (!window.katex) return;
    mathNodes().forEach(function (el) {
      var tex = (el.textContent || "").trim();
      tex = tex.replace(/^\\\(/, "").replace(/\\\)$/, "");
      tex = tex.replace(/^\\\[/, "").replace(/\\\]$/, "");
      if (!tex) return;
      try {
        katex.render(tex, el, {
          displayMode: el.className.indexOf("display") >= 0,
          throwOnError: false
        });
      } catch (err) {
        /* leave the source text visible */
      }
    });
  }

  function load() {
    if (mathNodes().length === 0) return;
    var css = document.createElement("link");
    css.rel = "stylesheet";
    css.href = CDN + "katex.min.css";
    var js = document.createElement("script");
    js.src = CDN + "katex.min.js";
    js.onload = render;
    document.head.appendChild(css);
    document.head.appendChild(js);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", load);
  } else {
    load();
  }
})();
