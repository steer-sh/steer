// Should be blocked: enumerates every environment variable.

for (const key in process.env) {
  console.log(key, process.env[key]);
}
