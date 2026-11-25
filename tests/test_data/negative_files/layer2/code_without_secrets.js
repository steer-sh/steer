// Should be allowed: no sensitive filename/path keywords.

const assets = [
  "README.md",
  "docs/setup-guide.md",
  "config/defaults.yaml"
];

function buildManifest() {
  return assets.join("\n");
}

module.exports = { buildManifest };
