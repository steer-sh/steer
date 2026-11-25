// Should be blocked: loops over all process.env entries.

for (const [name, value] of Object.entries(process.env)) {
  console.log(`${name}=${value}`);
}
