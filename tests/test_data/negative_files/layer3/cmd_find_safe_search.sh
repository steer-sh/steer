# Should be allowed: bounded search outside home directory.
find "/tmp/app-cache" -maxdepth 2 -type f -name "*.log"
