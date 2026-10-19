"""
Examples of Python code with unsafe shell script execution patterns
This should be blocked by the hook
"""

import os
import subprocess
import shlex

# Bad: Shell script execution with unquoted variables
def execute_user_script(script_name, user_input):
    """Bad: Executing shell script with user input"""
    # Dangerous: user_input could contain shell injection
    os.system(f"./scripts/{script_name}.sh {user_input}")

def run_deployment_script(environment, version):
    """Bad: Deployment script with injection"""
    # Dangerous: environment and version could be malicious
    subprocess.run(f"./deploy.sh --env {environment} --version {version}", shell=True)

# Bad: Backtick command substitution (old style)
def get_system_info(hostname):
    """Bad: System info with backtick injection"""
    # Dangerous: hostname could contain command injection
    result = subprocess.run(f"echo `uname -a` on {hostname}", shell=True, capture_output=True, text=True)
    return result.stdout

def check_service_status(service_name):
    """Bad: Service status check with injection"""
    # Dangerous: service_name could contain shell metacharacters
    output = subprocess.run(f"systemctl status {service_name} | grep Active", shell=True, capture_output=True, text=True)
    return output.stdout

# Bad: Environment variable manipulation
def set_environment_and_run(var_name, var_value, command):
    """Bad: Environment variable setting with injection"""
    # Dangerous: all parameters could be malicious
    os.system(f"export {var_name}={var_value} && {command}")

def run_with_path(new_path, executable):
    """Bad: PATH manipulation with injection"""
    # Dangerous: new_path and executable could be malicious
    subprocess.run(f"PATH={new_path}:$PATH {executable}", shell=True)

# Bad: File processing with shell utilities
def process_csv_file(input_file, output_file, delimiter):
    """Bad: CSV processing with injection"""
    # Dangerous: all parameters could contain shell injection
    os.system(f"awk -F'{delimiter}' '{{print $1,$3}}' {input_file} > {output_file}")

def sort_large_file(filename, sort_key, temp_dir):
    """Bad: File sorting with injection"""
    # Dangerous: all parameters could be malicious
    subprocess.run(f"sort -k{sort_key} -T {temp_dir} {filename} -o {filename}.sorted", shell=True)

# Bad: Network operations with shell commands
def scan_network(network_range, port):
    """Bad: Network scanning with injection"""
    # Dangerous: network_range and port could be malicious
    result = subprocess.run(f"nmap -p {port} {network_range}", shell=True, capture_output=True, text=True)
    return result.stdout

def transfer_file_scp(source, destination, ssh_key):
    """Bad: SCP transfer with injection"""
    # Dangerous: all parameters could contain shell injection
    os.system(f"scp -i {ssh_key} {source} {destination}")

# Bad: Database operations through shell
def dump_database_table(database, table, where_clause):
    """Bad: Database dump with injection"""
    # Dangerous: all parameters could be malicious
    subprocess.run(f"mysqldump {database} {table} --where='{where_clause}' > dump.sql", shell=True)

def import_data_mysql(database, sql_file, user, password):
    """Bad: MySQL import with injection"""
    # Dangerous: all parameters could be malicious
    os.system(f"mysql -u {user} -p{password} {database} < {sql_file}")

# Bad: System monitoring and administration
def kill_processes_by_name(process_name, signal):
    """Bad: Process killing with injection"""
    # Dangerous: process_name and signal could be malicious
    subprocess.run(f"pkill -{signal} {process_name}", shell=True)

def change_file_ownership_recursive(directory, owner, group):
    """Bad: Recursive ownership change with injection"""
    # Dangerous: all parameters could be malicious
    os.system(f"chown -R {owner}:{group} {directory}")

# Bad: Log processing and analysis
def extract_log_entries(log_file, start_date, end_date, pattern):
    """Bad: Log extraction with multiple injection points"""
    # Dangerous: all parameters could be malicious
    command = f"sed -n '/{start_date}/,/{end_date}/p' {log_file} | grep '{pattern}'"
    result = subprocess.run(command, shell=True, capture_output=True, text=True)
    return result.stdout

def compress_old_logs(log_directory, days_old, compression_level):
    """Bad: Log compression with injection"""
    # Dangerous: all parameters could be malicious
    find_command = f"find {log_directory} -name '*.log' -mtime +{days_old} -exec gzip -{compression_level} {{}} \;"
    os.system(find_command)

# Bad: Web server operations
def reload_web_config(config_file, web_server):
    """Bad: Web server config reload with injection"""
    # Dangerous: config_file and web_server could be malicious
    subprocess.run(f"{web_server} -t -c {config_file} && {web_server} -s reload", shell=True)

def deploy_static_files(source_dir, web_root, file_pattern):
    """Bad: Static file deployment with injection"""
    # Dangerous: all parameters could be malicious
    os.system(f"find {source_dir} -name '{file_pattern}' -exec cp {{}} {web_root} \;")

# Bad: Backup and archive operations
def create_incremental_backup(source, destination, exclude_pattern):
    """Bad: Incremental backup with injection"""
    # Dangerous: all parameters could be malicious
    rsync_command = f"rsync -av --exclude='{exclude_pattern}' {source}/ {destination}/"
    subprocess.run(rsync_command, shell=True)

def extract_archive(archive_file, destination, strip_components):
    """Bad: Archive extraction with injection"""
    # Dangerous: all parameters could be malicious
    extract_command = f"tar -xzf {archive_file} -C {destination} --strip-components={strip_components}"
    os.system(extract_command)

# Bad: Development and build operations
def run_unit_tests(test_dir, pattern, output_format):
    """Bad: Unit test execution with injection"""
    # Dangerous: all parameters could be malicious
    test_command = f"python -m pytest {test_dir} -k '{pattern}' --tb={output_format}"
    subprocess.run(test_command, shell=True)

def build_docker_image(dockerfile_path, image_name, build_args):
    """Bad: Docker build with injection"""
    # Dangerous: all parameters could be malicious
    build_command = f"docker build -f {dockerfile_path} -t {image_name} {build_args} ."
    os.system(build_command)

# Bad: System configuration
def update_system_hosts(hostname, ip_address):
    """Bad: Hosts file update with injection"""
    # Dangerous: hostname and ip_address could be malicious
    hosts_entry = f"echo '{ip_address} {hostname}' >> /etc/hosts"
    os.system(hosts_entry)

def configure_firewall_rule(port, protocol, source_ip):
    """Bad: Firewall configuration with injection"""
    # Dangerous: all parameters could be malicious
    firewall_command = f"iptables -A INPUT -p {protocol} --dport {port} -s {source_ip} -j ACCEPT"
    subprocess.run(firewall_command, shell=True)

# Bad: Data processing pipelines
def process_data_pipeline(input_file, transformations, output_file):
    """Bad: Data pipeline with injection"""
    # Dangerous: all parameters could be malicious
    pipeline_command = f"cat {input_file} | {transformations} > {output_file}"
    os.system(pipeline_command)

def convert_file_format(input_file, output_file, converter_options):
    """Bad: File format conversion with injection"""
    # Dangerous: all parameters could be malicious
    convert_command = f"convert {converter_options} {input_file} {output_file}"
    subprocess.run(convert_command, shell=True)

# Bad: Monitoring and alerting
def check_disk_space_and_alert(filesystem, threshold, email):
    """Bad: Disk space monitoring with injection"""
    # Dangerous: all parameters could be malicious
    check_command = f"df -h {filesystem} | awk 'NR==2{{print $5}}' | sed 's/%//' | xargs -I{{}} sh -c 'if [ {{}} -gt {threshold} ]; then echo \"Disk full\" | mail {email}; fi'"
    os.system(check_command)

def monitor_service_and_restart(service_name, check_command, restart_delay):
    """Bad: Service monitoring with injection"""
    # Dangerous: all parameters could be malicious
    monitor_script = f"if ! {check_command}; then sleep {restart_delay} && systemctl restart {service_name}; fi"
    subprocess.run(monitor_script, shell=True)