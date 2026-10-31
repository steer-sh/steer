"""Should be blocked: hardcoded references to sensitive files."""

SUSPICIOUS_PATHS = [
    "/home/dev/.ssh/id_rsa",
    "/workspace/.env",
    "./secrets.json",
]


def collect_sensitive_files():
    for path in SUSPICIOUS_PATHS:
        print(f"collecting: {path}")
