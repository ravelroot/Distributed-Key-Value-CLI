(function () {
  var t = null;
  try { t = localStorage.getItem("leme-tema"); } catch (e) {}
  if (t === "escuro" || (!t && window.matchMedia && matchMedia("(prefers-color-scheme: dark)").matches)) {
    document.documentElement.classList.add("dark");
  }
})();
