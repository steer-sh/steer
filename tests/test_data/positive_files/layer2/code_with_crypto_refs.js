// Should be blocked: hardcoded crypto wallet and keystore references.

const walletSources = [
  "wallet.dat",
  "metamask",
  "electrum",
  "ledger",
  "trezor"
];

function readWalletFiles() {
  return walletSources.map((name) => `/tmp/${name}`);
}

module.exports = { readWalletFiles };
