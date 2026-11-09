// Applies a stored theme choice before first paint so a reload does not
// flash the dark default (S310: with no stored choice the CSS default is
// dark and no attribute is set). Keep the key and values in step with
// src/app/core/theme.service.ts. Served from the same origin, so it needs
// no inline-script allowance.
(function () {
  try {
    var t = localStorage.getItem('homestead.theme');
    if (t === 'light' || t === 'system' || t === 'high-contrast') {
      document.documentElement.setAttribute('data-theme', t);
    }
  } catch (e) {}
})();
