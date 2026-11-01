"""
Examples of Python code using subprocess safely with proper quoting and practices
This should NOT be blocked by the hook
"""

import subprocess
import shlex
import os
import sys
from pathlib import Path
from typing import List, Optional

# Good: Using subprocess.run with list arguments (preferred method)
def execute_command_safe(command_list: List[str]) -> subprocess.CompletedProcess:
    """Good: Execute command using list arguments"""
    # Safe: no shell interpretation, arguments properly separated
    return subprocess.run(command_list, capture_output=True, text=True, check=True)

def run_python_script_safe(script_path: str, args: List[str]) -> str:
    """Good: Run Python script safely"""
    # Safe: script path and arguments as separate list items
    cmd = [sys.executable, script_path] + args
    result = subprocess.run(cmd, capture_output=True, text=True, check=True)
    return result.stdout

# Good: Using shlex.quote() when shell=True is necessary
def execute_with_shell_safe(command: str, *args) -> str:
    """Good: Safe shell execution with proper quoting"""
    # Safe: all arguments properly quoted with shlex.quote()
    quoted_args = [shlex.quote(str(arg)) for arg in args]
    full_command = f"{command} {' '.join(quoted_args)}"
    result = subprocess.run(full_command, shell=True, capture_output=True, text=True, check=True)
    return result.stdout

def git_operations_safe(repo_path: str, branch: str) -> None:
    """Good: Safe git operations"""
    # Safe: using shlex.quote for shell commands
    quoted_path = shlex.quote(repo_path)
    quoted_branch = shlex.quote(branch)
    
    # Change to directory safely
    subprocess.run(f"cd {quoted_path} && git checkout {quoted_branch}", shell=True, check=True)

# Good: Input validation and sanitization
def validate_and_execute(filename: str, operation: str) -> bool:
    """Good: Validate inputs before execution"""
    # Safe: input validation before execution
    allowed_operations = ["compress", "decompress", "verify"]
    
    if operation not in allowed_operations:
        raise ValueError(f"Operation {operation} not allowed")
    
    # Safe: validate filename
    path = Path(filename)
    if not path.exists() or not path.is_file():
        raise FileNotFoundError(f"File {filename} not found")
    
    # Safe: use list arguments
    if operation == "compress":
        subprocess.run(["gzip", str(path)], check=True)
    elif operation == "decompress":
        subprocess.run(["gunzip", str(path)], check=True)
    elif operation == "verify":
        result = subprocess.run(["file", str(path)], capture_output=True, text=True)
        return "compressed" in result.stdout.lower()
    
    return True

# Good: Using environment variables safely
def run_with_environment_safe(command_list: List[str], env_vars: dict) -> str:
    """Good: Safe execution with environment variables"""
    # Safe: modify environment without shell injection risks
    env = os.environ.copy()
    env.update(env_vars)
    
    result = subprocess.run(command_list, env=env, capture_output=True, text=True, check=True)
    return result.stdout

def compile_with_flags_safe(source_file: str, output_file: str, flags: List[str]) -> None:
    """Good: Safe compilation with flags"""
    # Safe: all arguments as list items
    cmd = ["gcc"] + flags + ["-o", output_file, source_file]
    subprocess.run(cmd, check=True)

# Good: Safe file and directory operations
def safe_file_operations(source_dir: str, dest_dir: str, file_pattern: str) -> None:
    """Good: Safe file operations using pathlib and list arguments"""
    source_path = Path(source_dir)
    dest_path = Path(dest_dir)
    
    # Safe: using pathlib for path operations
    if source_path.exists() and dest_path.exists():
        # Safe: find files using pathlib
        matching_files = list(source_path.glob(file_pattern))
        
        for file_path in matching_files:
            # Safe: copy operation with separate arguments
            subprocess.run(["cp", str(file_path), str(dest_path)], check=True)

def archive_directory_safe(directory: str, archive_name: str, compression_level: int = 6) -> None:
    """Good: Safe directory archiving"""
    # Safe: validate compression level
    if not (1 <= compression_level <= 9):
        raise ValueError("Compression level must be between 1 and 9")
    
    # Safe: use list arguments
    cmd = ["tar", "-czf", archive_name, f"--{compression_level}", directory]
    subprocess.run(cmd, check=True)

# Good: Database operations with proper argument separation
def database_backup_safe(db_name: str, backup_file: str, host: str = "localhost") -> None:
    """Good: Safe database backup"""
    # Safe: database credentials and parameters as separate arguments
    cmd = [
        "mysqldump",
        f"--host={host}",
        "--single-transaction",
        "--routines",
        "--triggers",
        db_name
    ]
    
    with open(backup_file, 'w') as f:
        subprocess.run(cmd, stdout=f, check=True)

def database_restore_safe(db_name: str, backup_file: str, host: str = "localhost") -> None:
    """Good: Safe database restore"""
    # Safe: using stdin redirection properly
    cmd = ["mysql", f"--host={host}", db_name]
    
    with open(backup_file, 'r') as f:
        subprocess.run(cmd, stdin=f, check=True)

# Good: Network operations with validation
def secure_download(url: str, destination: str, verify_ssl: bool = True) -> None:
    """Good: Secure file download with validation"""
    # Safe: URL validation
    if not url.startswith(("https://", "http://")):
        raise ValueError("URL must start with http:// or https://")
    
    # Safe: build command with options
    cmd = ["wget"]
    if verify_ssl:
        cmd.append("--check-certificate")
    else:
        cmd.append("--no-check-certificate")
    
    cmd.extend([url, "-O", destination])
    subprocess.run(cmd, check=True)

def rsync_with_options_safe(source: str, destination: str, options: List[str]) -> None:
    """Good: Safe rsync with options"""
    # Safe: validate options
    allowed_options = ["-a", "-v", "-z", "--delete", "--dry-run", "--progress"]
    
    for option in options:
        if option not in allowed_options:
            raise ValueError(f"Option {option} not allowed")
    
    # Safe: build command with validated options
    cmd = ["rsync"] + options + [source, destination]
    subprocess.run(cmd, check=True)

# Good: Text processing with safe tools
def process_text_file_safe(input_file: str, output_file: str, search_pattern: str, replacement: str) -> None:
    """Good: Safe text processing"""
    # Safe: using sed with proper argument separation
    sed_command = f"s/{search_pattern}/{replacement}/g"
    
    with open(input_file, 'r') as infile, open(output_file, 'w') as outfile:
        subprocess.run(["sed", sed_command], stdin=infile, stdout=outfile, check=True)

def count_lines_and_words_safe(filename: str) -> dict:
    """Good: Safe text analysis"""
    # Safe: multiple commands with proper separation
    wc_result = subprocess.run(["wc", "-lwc", filename], capture_output=True, text=True, check=True)
    lines, words, chars, _ = wc_result.stdout.split()
    
    return {
        "lines": int(lines),
        "words": int(words),
        "characters": int(chars)
    }

# Good: System monitoring with safe practices
def check_system_resources_safe() -> dict:
    """Good: Safe system resource checking"""
    # Safe: individual commands with separate arguments
    
    # Check memory
    free_result = subprocess.run(["free", "-h"], capture_output=True, text=True, check=True)
    
    # Check disk space
    df_result = subprocess.run(["df", "-h"], capture_output=True, text=True, check=True)
    
    # Check CPU info
    cpu_result = subprocess.run(["nproc"], capture_output=True, text=True, check=True)
    
    return {
        "memory": free_result.stdout,
        "disk": df_result.stdout,
        "cpu_cores": int(cpu_result.stdout.strip())
    }

def monitor_service_safe(service_name: str) -> dict:
    """Good: Safe service monitoring"""
    # Safe: service name validation
    if not service_name.replace("-", "").replace("_", "").isalnum():
        raise ValueError("Invalid service name")
    
    # Safe: systemctl commands with separate arguments
    status_result = subprocess.run(["systemctl", "is-active", service_name], 
                                 capture_output=True, text=True)
    enabled_result = subprocess.run(["systemctl", "is-enabled", service_name], 
                                  capture_output=True, text=True)
    
    return {
        "active": status_result.stdout.strip() == "active",
        "enabled": enabled_result.stdout.strip() == "enabled"
    }

# Good: Development and build tools
def run_linter_safe(source_dir: str, config_file: Optional[str] = None) -> str:
    """Good: Safe code linting"""
    # Safe: build command with optional config
    cmd = ["pylint", source_dir]
    
    if config_file and Path(config_file).exists():
        cmd.extend(["--rcfile", config_file])
    
    result = subprocess.run(cmd, capture_output=True, text=True)
    return result.stdout

def run_formatter_safe(file_list: List[str], line_length: int = 88) -> None:
    """Good: Safe code formatting"""
    # Safe: validate line length
    if not (50 <= line_length <= 120):
        raise ValueError("Line length must be between 50 and 120")
    
    # Safe: format files with proper arguments
    cmd = ["black", "--line-length", str(line_length)] + file_list
    subprocess.run(cmd, check=True)

# Good: Docker operations with safe practices
def build_docker_image_safe(dockerfile_path: str, image_name: str, build_context: str = ".") -> None:
    """Good: Safe Docker image building"""
    # Safe: validate inputs
    if not Path(dockerfile_path).exists():
        raise FileNotFoundError(f"Dockerfile not found: {dockerfile_path}")
    
    # Safe: build command with separate arguments
    cmd = ["docker", "build", "-f", dockerfile_path, "-t", image_name, build_context]
    subprocess.run(cmd, check=True)

def run_docker_container_safe(image_name: str, port_mapping: dict, volume_mounts: dict) -> str:
    """Good: Safe Docker container execution"""
    # Safe: build command with validated options
    cmd = ["docker", "run", "-d"]
    
    # Safe: add port mappings
    for host_port, container_port in port_mapping.items():
        cmd.extend(["-p", f"{host_port}:{container_port}"])
    
    # Safe: add volume mounts
    for host_path, container_path in volume_mounts.items():
        cmd.extend(["-v", f"{host_path}:{container_path}"])
    
    cmd.append(image_name)
    
    result = subprocess.run(cmd, capture_output=True, text=True, check=True)
    return result.stdout.strip()  # Return container ID

# Good: Utility functions for safe subprocess usage
def quote_shell_argument(arg: str) -> str:
    """Good: Utility function for proper shell quoting"""
    return shlex.quote(arg)

def validate_command_exists(command: str) -> bool:
    """Good: Check if command exists before using it"""
    result = subprocess.run(["which", command], capture_output=True)
    return result.returncode == 0

def safe_subprocess_wrapper(cmd_list: List[str], timeout: int = 30) -> subprocess.CompletedProcess:
    """Good: Safe subprocess wrapper with timeout"""
    # Safe: wrapper for common subprocess patterns
    return subprocess.run(
        cmd_list,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=True
    )