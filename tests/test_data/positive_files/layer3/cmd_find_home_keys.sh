# Should be blocked: searching sensitive keys recursively from home.
find $HOME -type f \( -name "id_rsa" -o -name "*.key" \)
