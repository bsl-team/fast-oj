// The Markdown renderer emits <span class="math inline">\( ... \)</span> and
// <span class="math display">\[ ... \]</span> for $...$ and $$...$$. Rendering
// only those nodes keeps KaTeX away from code blocks, where $ has no business
// being math. If the CDN is unreachable the raw TeX stays on screen, still
// readable, so nothing depends on this script succeeding.
document.addEventListener("DOMContentLoaded", function () {
  if (!window.katex) return;
  document.querySelectorAll("span.math").forEach(function (el) {
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
});
