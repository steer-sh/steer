"""Should be allowed: only normal project/config references."""

SAFE_PATHS = [
    "/workspace/project/config/settings.yaml",
    "/workspace/project/logs/service.log",
    "./docs/architecture.md",
]


def list_safe_files():
    return SAFE_PATHS
