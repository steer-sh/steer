// Should be blocked: uses Object.keys(process.env).

const envKeys = Object.keys(process.env);
envKeys.forEach((key) => {
  console.log(key);
});
