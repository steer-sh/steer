// Should be allowed: configuration object with no env enumeration.

const defaults = {
  retryCount: 3,
  backoffMs: 200,
  metricsEnabled: true,
};

function getConfig() {
  return {
    ...defaults,
    mode: process.env.APP_MODE,
  };
}

module.exports = { getConfig };
