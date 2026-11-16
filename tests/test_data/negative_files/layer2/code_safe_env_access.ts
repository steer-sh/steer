// Should be allowed: explicit environment variable reads only.

const appConfig = {
  apiUrl: process.env.API_URL,
  logLevel: process.env.LOG_LEVEL,
  timeoutMs: Number(process.env.TIMEOUT_MS || 5000),
};

export default appConfig;
