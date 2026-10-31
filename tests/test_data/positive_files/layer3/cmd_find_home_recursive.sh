# Should be blocked: recursive search from home directory.
find $HOME -type f -name "*.pem"
