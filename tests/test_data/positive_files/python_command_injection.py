"""
Examples of Python code with command injection vulnerabilities
This should be blocked by the hook
"""

import os
import subprocess
import commands  # Python 2 style, but still dangerous

# Bad: Using os.system with unquoted variables
def delete_user_files(username):
    """Bad: Command injection via os.system"""
    # Dangerous: username could contain "; rm -rf /"
    os.system(f"rm -rf /home/{username}")

def backup_database(db_name):
    """Bad: Command injection in backup command"""
    # Dangerous: db_name could contain shell metacharacters
    os.system(f"mysqldump {db_name} > backup.sql")

def process_file(filename):
    """Bad: File processing with command injection"""
    # Dangerous: filename could contain "; cat /etc/passwd"
    os.system(f"wc -l {filename}")

# Bad: Using subprocess.run with shell=True and unquoted variables
def git_clone_repo(repo_url):
    """Bad: Git clone with potential injection"""
    # Dangerous: repo_url could contain "; rm -rf /"
    subprocess.run(f"git clone {repo_url}", shell=True)

def compress_directory(directory):
    """Bad: Compression with shell injection"""
    # Dangerous: directory could contain shell metacharacters
    subprocess.run(f"tar -czf archive.tar.gz {directory}", shell=True)

def search_files(pattern, directory):
    """Bad: File search with multiple injection points"""
    # Dangerous: both pattern and directory could be malicious
    subprocess.run(f"grep -r '{pattern}' {directory}", shell=True)

# Bad: Using subprocess.call with shell=True
def convert_image(input_file, output_file):
    """Bad: Image conversion with injection"""
    # Dangerous: file paths could contain shell metacharacters
    subprocess.call(f"convert {input_file} {output_file}", shell=True)

def install_package(package_name):
    """Bad: Package installation with injection"""
    # Dangerous: package_name could contain malicious commands
    subprocess.call(f"pip install {package_name}", shell=True)

# Bad: Using subprocess.Popen with shell=True
def run_user_script(script_path, args):
    """Bad: Running user script with injection"""
    # Dangerous: script_path and args could be malicious
    process = subprocess.Popen(f"python {script_path} {args}", shell=True)
    return process.communicate()

def tail_log_file(filename, lines):
    """Bad: Log tailing with injection"""
    # Dangerous: both filename and lines could be malicious
    process = subprocess.Popen(f"tail -n {lines} {filename}", shell=True, stdout=subprocess.PIPE)
    return process.stdout.read()

# Bad: Using os.popen
def get_disk_usage(path):
    """Bad: Disk usage check with injection"""
    # Dangerous: path could contain shell metacharacters
    with os.popen(f"du -sh {path}") as pipe:
        return pipe.read()

def list_processes(user):
    """Bad: Process listing with injection"""
    # Dangerous: user could contain shell injection
    with os.popen(f"ps aux | grep {user}") as pipe:
        return pipe.read()

# Bad: Using backticks (subprocess with shell=True)
def ping_host(hostname):
    """Bad: Network ping with injection"""
    # Dangerous: hostname could contain shell commands
    result = subprocess.run(f"`ping -c 4 {hostname}`", shell=True, capture_output=True, text=True)
    return result.stdout

# Bad: Command injection in web applications
def handle_user_upload(filename, user_id):
    """Bad: File handling with command injection"""
    # Dangerous: filename could contain shell injection
    os.system(f"chown {user_id} /uploads/{filename}")
    os.system(f"chmod 644 /uploads/{filename}")

def generate_thumbnail(image_path, size):
    """Bad: Image thumbnail generation"""
    # Dangerous: image_path and size could be malicious
    subprocess.run(f"convert {image_path} -resize {size} thumbnail.jpg", shell=True)

# Bad: Database operations with command injection
def export_user_data(user_id, format_type):
    """Bad: Data export with injection"""
    # Dangerous: user_id and format_type could be malicious
    if format_type == "csv":
        os.system(f"mysql -e 'SELECT * FROM users WHERE id={user_id}' --batch > export.csv")
    elif format_type == "json":
        os.system(f"mysql -e 'SELECT * FROM users WHERE id={user_id}' --json > export.json")

def backup_user_directory(username, backup_path):
    """Bad: User directory backup with injection"""
    # Dangerous: both username and backup_path could be malicious
    subprocess.run(f"rsync -av /home/{username}/ {backup_path}/", shell=True)

# Bad: Log analysis with command injection
def analyze_logs(log_file, search_term):
    """Bad: Log analysis with injection"""
    # Dangerous: both log_file and search_term could be malicious
    result = subprocess.run(f"grep '{search_term}' {log_file} | wc -l", shell=True, capture_output=True, text=True)
    return int(result.stdout.strip())

def rotate_logs(log_path, max_size):
    """Bad: Log rotation with injection"""
    # Dangerous: log_path and max_size could be malicious
    os.system(f"find {log_path} -size +{max_size} -exec gzip {{}} \;")

# Bad: System administration tasks
def create_user_account(username, password):
    """Bad: User account creation with injection"""
    # Dangerous: username and password could contain shell metacharacters
    os.system(f"useradd -m {username}")
    os.system(f"echo '{username}:{password}' | chpasswd")

def set_file_permissions(file_path, permissions, owner):
    """Bad: File permission setting with injection"""
    # Dangerous: all parameters could be malicious
    os.system(f"chmod {permissions} {file_path}")
    os.system(f"chown {owner} {file_path}")

# Bad: Network operations
def download_file(url, destination):
    """Bad: File download with injection"""
    # Dangerous: url and destination could be malicious
    subprocess.run(f"wget {url} -O {destination}", shell=True)

def sync_remote_directory(remote_host, remote_path, local_path):
    """Bad: Remote sync with injection"""
    # Dangerous: all parameters could contain injection
    subprocess.run(f"rsync -av {remote_host}:{remote_path} {local_path}", shell=True)

# Bad: Development tools
def run_tests(test_pattern, coverage_output):
    """Bad: Test execution with injection"""
    # Dangerous: test_pattern and coverage_output could be malicious
    subprocess.run(f"pytest {test_pattern} --cov-report={coverage_output}", shell=True)

def build_project(build_target, output_dir):
    """Bad: Project build with injection"""
    # Dangerous: build_target and output_dir could be malicious
    os.system(f"make {build_target} OUTPUT_DIR={output_dir}")

# Bad: Multiple command patterns
def complex_file_operation(input_file, output_file, options):
    """Bad: Complex operation with multiple injection points"""
    # Dangerous: all parameters could be malicious
    temp_file = f"/tmp/processing_{input_file}"
    
    # Multiple vulnerable operations
    os.system(f"cp {input_file} {temp_file}")
    subprocess.run(f"sed {options} {temp_file} > {output_file}", shell=True)
    os.system(f"rm {temp_file}")

# Bad: Using format strings in commands
def format_disk(device, filesystem):
    """Bad: Disk formatting with injection"""
    # Dangerous: device and filesystem could be malicious
    command = f"mkfs.{filesystem} {device}"
    os.system(command)

def mount_filesystem(device, mountpoint, options):
    """Bad: Filesystem mounting with injection"""
    # Dangerous: all parameters could be malicious
    mount_command = f"mount -t ext4 -o {options} {device} {mountpoint}"
    subprocess.run(mount_command, shell=True)