// Injected into the page's MAIN world so the bridge can report real console
// output and uncaught errors to the executor. Content scripts live in an
// isolated world and never see the page's own console calls — this tap does.
(() => {
  if (window.__tyConsoleTap) return;
  window.__tyConsoleTap = true;

  const post = (level, args) => {
    const text = args
      .map((a) => {
        if (typeof a === 'string') return a;
        try {
          return JSON.stringify(a);
        } catch {
          return String(a);
        }
      })
      .join(' ');
    window.postMessage({ __tyConsole: { level, text: text.slice(0, 500), ts: Date.now() } }, '*');
  };

  for (const level of ['log', 'info', 'warn', 'error', 'debug']) {
    const orig = console[level];
    console[level] = (...args) => {
      post(level, args);
      orig.apply(console, args);
    };
  }
  window.addEventListener('error', (e) => post('error', [`${e.message} @ ${e.filename}:${e.lineno}`]));
  window.addEventListener('unhandledrejection', (e) => post('error', ['Unhandled rejection: ' + e.reason]));
})();
