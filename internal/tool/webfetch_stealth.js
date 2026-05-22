// @prompto-stealth medium
//
// Patches applied per the Phase-15 medium stealth set. Runs on every
// frame on every navigation via Page.addScriptToEvaluateOnNewDocument
// — wins the race against page scripts.
(() => {
  // navigator.webdriver - the single most-tested property for
  // automation detection. Set to undefined to match real Chrome.
  try {
    Object.defineProperty(navigator, 'webdriver', {
      get: () => undefined,
      configurable: true,
    });
  } catch (_) {}

  // navigator.languages - must agree with Accept-Language.
  try {
    Object.defineProperty(navigator, 'languages', {
      get: () => ['en-US', 'en'],
      configurable: true,
    });
  } catch (_) {}

  // navigator.plugins - real Chrome reports a non-empty PluginArray
  // even in headless mode (since Chrome 113+). Stub a plausible set.
  try {
    const fakePlugins = [
      { name: 'PDF Viewer', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
      { name: 'Chrome PDF Viewer', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
      { name: 'Chromium PDF Viewer', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
      { name: 'Microsoft Edge PDF Viewer', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
      { name: 'WebKit built-in PDF', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
    ];
    Object.defineProperty(navigator, 'plugins', {
      get: () => fakePlugins,
      configurable: true,
    });
    Object.defineProperty(navigator, 'mimeTypes', {
      get: () => [
        { type: 'application/pdf', description: 'Portable Document Format', suffixes: 'pdf' },
        { type: 'text/pdf', description: 'Portable Document Format', suffixes: 'pdf' },
      ],
      configurable: true,
    });
  } catch (_) {}

  // navigator.userAgentData - the modern client-hint API. Values must
  // match the Sec-CH-UA* headers we send.
  try {
    const brands = [
      { brand: 'Chromium', version: '145' },
      { brand: 'Not;A=Brand', version: '24' },
      { brand: 'Google Chrome', version: '145' },
    ];
    const fullVersionList = [
      { brand: 'Chromium', version: '145.0.0.0' },
      { brand: 'Not;A=Brand', version: '24.0.0.0' },
      { brand: 'Google Chrome', version: '145.0.0.0' },
    ];
    Object.defineProperty(navigator, 'userAgentData', {
      get: () => ({
        brands,
        mobile: false,
        platform: 'Linux',
        getHighEntropyValues: (hints) => Promise.resolve({
          architecture: 'x86',
          bitness: '64',
          brands,
          fullVersionList,
          mobile: false,
          model: '',
          platform: 'Linux',
          platformVersion: '6.6.0',
          uaFullVersion: '145.0.0.0',
          wow64: false,
        }),
        toJSON: () => ({ brands, mobile: false, platform: 'Linux' }),
      }),
      configurable: true,
    });
  } catch (_) {}

  // window.chrome - real Chrome exposes this object even in headless
  // mode. Without it, `typeof window.chrome === 'undefined'` is a
  // headless tell.
  try {
    if (!window.chrome) {
      Object.defineProperty(window, 'chrome', {
        get: () => ({
          runtime: {},
          app: { isInstalled: false },
          csi: () => ({ onloadT: Date.now(), pageT: 0, startE: Date.now(), tran: 15 }),
          loadTimes: () => ({}),
        }),
        configurable: true,
      });
    }
  } catch (_) {}

  // Permissions.query - real Chrome returns Notification.permission's
  // current state when queried for 'notifications'. Headless used to
  // diverge here; the patch realigns it.
  try {
    const originalQuery = window.navigator.permissions && window.navigator.permissions.query;
    if (originalQuery) {
      window.navigator.permissions.query = (parameters) =>
        parameters && parameters.name === 'notifications'
          ? Promise.resolve({ state: Notification.permission, onchange: null })
          : originalQuery(parameters);
    }
  } catch (_) {}
})();
