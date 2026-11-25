"""
Examples of Python code with safe command execution practices
This should NOT be blocked by the hook
"""

import subprocess
import shlex
import os
from pathlib import Path

# Good: Using subprocess with list arguments (no shell=True)
def delete_user_files_safe(username):
    """Good: Safe file deletion using list arguments"""
    # Safe: username is properly quoted as a separate argument
    user_home = f"/home/{username}"
    if os.path.exists(user_home):
        subprocess.run(["rm", "-rf", user_home], check=True)

def backup_database_safe(db_name):
    """Good: Safe database backup"""
    # Safe: db_name is a separate argument, no shell interpretation
    output_file = f"{db_name}_backup.sql"
    subprocess.run(["mysqldump", db_name], stdout=open(output_file, 'w'), check=True)

def process_file_safe(filename):
    """Good: Safe file processing"""
    # Safe: filename is properly separated
    result = subprocess.run(["wc", "-l", filename], capture_output=True, text=True, check=True)
    return int(result.stdout.strip())

# Good: Using shlex.quote() for shell commands when necessary
def git_clone_repo_safe(repo_url):
    """Good: Safe git clone with proper quoting"""
    # Safe: repo_url is properly quoted
    quoted_url = shlex.quote(repo_url)
    subprocess.run(f"git clone {quoted_url}", shell=True, check=True)

def compress_directory_safe(directory):
    """Good: Safe compression with proper quoting"""
    # Safe: directory path is properly quoted
    quoted_dir = shlex.quote(directory)
    subprocess.run(f"tar -czf archive.tar.gz {quoted_dir}", shell=True, check=True)

def search_files_safe(pattern, directory):
    """Good: Safe file search using list arguments"""
    # Safe: using list arguments avoids shell interpretation
    result = subprocess.run(["grep", "-r", pattern, directory], capture_output=True, text=True)
    return result.stdout

# Good: Using pathlib and proper argument separation
def convert_image_safe(input_file, output_file):
    """Good: Safe image conversion"""
    # Safe: file paths as separate arguments
    input_path = Path(input_file)
    output_path = Path(output_file)
    
    if input_path.exists():
        subprocess.run(["convert", str(input_path), str(output_path)], check=True)

def install_package_safe(package_name):
    """Good: Safe package installation"""
    # Safe: package name as separate argument
    # Additional validation could be added here
    if package_name.replace("-", "").replace("_", "").isalnum():
        subprocess.run(["pip", "install", package_name], check=True)

# Good: Using proper argument lists with subprocess.Popen
def run_user_script_safe(script_path, args_list):
    """Good: Safe script execution with argument list"""
    # Safe: script path and arguments as separate list items
    script = Path(script_path)
    if script.exists() and script.suffix == '.py':
        cmd = ["python", str(script)] + args_list
        process = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return process.communicate()

def tail_log_file_safe(filename, lines):
    """Good: Safe log tailing"""
    # Safe: arguments separated and validated
    if isinstance(lines, int) and lines > 0:
        result = subprocess.run(["tail", "-n", str(lines), filename], 
                              capture_output=True, text=True, check=True)
        return result.stdout

# Good: Safe file operations without shell
def get_disk_usage_safe(path):
    """Good: Safe disk usage check"""
    # Safe: path as separate argument
    result = subprocess.run(["du", "-sh", path], capture_output=True, text=True, check=True)
    return result.stdout.strip()

def list_processes_safe(user):
    """Good: Safe process listing"""
    # Safe: using separate subprocess calls
    ps_result = subprocess.run(["ps", "aux"], capture_output=True, text=True, check=True)
    grep_result = subprocess.run(["grep", user], input=ps_result.stdout, 
                                capture_output=True, text=True)
    return grep_result.stdout

# Good: Network operations with proper validation
def ping_host_safe(hostname):
    """Good: Safe network ping"""
    # Safe: hostname validation and separate arguments
    if hostname.replace(".", "").replace("-", "").isalnum():
        result = subprocess.run(["ping", "-c", "4", hostname], 
                              capture_output=True, text=True, check=True)
        return result.stdout

# Good: File handling with proper validation
def handle_user_upload_safe(filename, user_id):
    """Good: Safe file handling"""
    # Safe: validate inputs and use separate arguments
    if isinstance(user_id, int) and filename.replace(".", "").replace("_", "").isalnum():
        upload_path = f"/uploads/{filename}"
        if os.path.exists(upload_path):
            subprocess.run(["chown", str(user_id), upload_path], check=True)
            subprocess.run(["chmod", "644", upload_path], check=True)

def generate_thumbnail_safe(image_path, size):
    """Good: Safe image thumbnail generation"""
    # Safe: arguments separated and validated
    if size.replace("x", "").isdigit():
        subprocess.run(["convert", image_path, "-resize", size, "thumbnail.jpg"], check=True)

# Good: Database operations with safe practices
def export_user_data_safe(user_id, format_type):
    """Good: Safe data export"""
    # Safe: using separate arguments and validation
    if isinstance(user_id, int) and format_type in ["csv", "json"]:
        query = f"SELECT * FROM users WHERE id={user_id}"
        
        if format_type == "csv":
            subprocess.run(["mysql", "-e", query, "--batch"], 
                         stdout=open("export.csv", "w"), check=True)
        elif format_type == "json":
            subprocess.run(["mysql", "-e", query, "--json"], 
                         stdout=open("export.json", "w"), check=True)

def backup_user_directory_safe(username, backup_path):
    """Good: Safe directory backup"""
    # Safe: paths as separate arguments
    source_path = f"/home/{username}/"
    if os.path.exists(source_path) and os.path.exists(backup_path):
        subprocess.run(["rsync", "-av", source_path, f"{backup_path}/"], check=True)

# Good: Log analysis with safe practices
def analyze_logs_safe(log_file, search_term):
    """Good: Safe log analysis"""
    # Safe: using separate subprocess calls
    if os.path.exists(log_file):
        grep_result = subprocess.run(["grep", search_term, log_file], 
                                   capture_output=True, text=True)
        wc_result = subprocess.run(["wc", "-l"], input=grep_result.stdout, 
                                 capture_output=True, text=True)
        return int(wc_result.stdout.strip())

def rotate_logs_safe(log_path, max_size_mb):
    """Good: Safe log rotation"""
    # Safe: using pathlib and separate arguments
    if isinstance(max_size_mb, int) and max_size_mb > 0:
        max_size_bytes = max_size_mb * 1024 * 1024
        log_dir = Path(log_path)
        
        for log_file in log_dir.glob("*.log"):
            if log_file.stat().st_size > max_size_bytes:
                subprocess.run(["gzip", str(log_file)], check=True)

# Good: System administration with validation
def create_user_account_safe(username, password):
    """Good: Safe user account creation"""
    # Safe: username validation and separate arguments
    if username.isalnum() and len(username) <= 32:
        subprocess.run(["useradd", "-m", username], check=True)
        
        # Use subprocess for password setting
        process = subprocess.Popen(["chpasswd"], stdin=subprocess.PIPE, text=True)
        process.communicate(f"{username}:{password}")

def set_file_permissions_safe(file_path, permissions, owner):
    """Good: Safe file permission setting"""
    # Safe: validate inputs and use separate arguments
    if permissions.isdigit() and len(permissions) == 3:
        if os.path.exists(file_path):
            subprocess.run(["chmod", permissions, file_path], check=True)
            subprocess.run(["chown", owner, file_path], check=True)

# Good: Network operations with validation
def download_file_safe(url, destination):
    """Good: Safe file download"""
    # Safe: URL validation and separate arguments
    if url.startswith(("http://", "https://")) and destination:
        dest_path = Path(destination)
        subprocess.run(["wget", url, "-O", str(dest_path)], check=True)

def sync_remote_directory_safe(remote_host, remote_path, local_path):
    """Good: Safe remote sync"""
    # Safe: validate inputs and use separate arguments
    if all([remote_host, remote_path, local_path]):
        source = f"{remote_host}:{remote_path}"
        subprocess.run(["rsync", "-av", source, local_path], check=True)

# Good: Development tools with safe execution
def run_tests_safe(test_pattern, coverage_output):
    """Good: Safe test execution"""
    # Safe: arguments as separate list items
    cmd = ["pytest"]
    if test_pattern:
        cmd.extend(["-k", test_pattern])
    if coverage_output:
        cmd.extend([f"--cov-report={coverage_output}"])
    
    subprocess.run(cmd, check=True)

def build_project_safe(build_target, output_dir):
    """Good: Safe project build"""
    # Safe: using environment variables instead of shell interpolation
    env = os.environ.copy()
    if output_dir:
        env["OUTPUT_DIR"] = output_dir
    
    subprocess.run(["make", build_target], env=env, check=True)

# Good: Using environment variables and proper validation
def format_disk_safe(device, filesystem):
    """Good: Safe disk formatting with validation"""
    # Safe: validate device path and filesystem type
    allowed_filesystems = ["ext4", "ext3", "xfs"]
    if filesystem in allowed_filesystems and device.startswith("/dev/"):
        subprocess.run([f"mkfs.{filesystem}", device], check=True)

def mount_filesystem_safe(device, mountpoint, options_list):
    """Good: Safe filesystem mounting"""
    # Safe: options as separate list items
    if device.startswith("/dev/") and os.path.exists(mountpoint):
        cmd = ["mount", "-t", "ext4"]
        if options_list:
            cmd.extend(["-o", ",".join(options_list)])
        cmd.extend([device, mountpoint])
        subprocess.run(cmd, check=True)

# Good: Using proper Python libraries instead of shell commands
def get_system_info_safe():
    """Good: Using Python libraries instead of shell commands"""
    import platform
    import socket
    
    return {
        "hostname": socket.gethostname(),
        "platform": platform.platform(),
        "python_version": platform.python_version(),
        "architecture": platform.architecture()
    }

def check_service_status_safe(service_name):
    """Good: Safe service status check"""
    # Safe: service name validation and separate arguments
    if service_name.replace("-", "").replace("_", "").isalnum():
        result = subprocess.run(["systemctl", "status", service_name], 
                              capture_output=True, text=True)
        return "active" in result.stdout