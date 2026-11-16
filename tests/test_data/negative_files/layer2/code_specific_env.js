// Should be allowed: accesses only specific named environment variables.

const config = {
  nodeEnv: process.env.NODE_ENV,
  port: process.env.PORT,
  region: process.env.AWS_REGION,
};

module.exports = config;
