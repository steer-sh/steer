# Python code consistently using f-strings throughout (should be ALLOWED)

import datetime
import json

class APIClient:
    def __init__(self, base_url, api_key):
        self.base_url = base_url
        self.api_key = api_key
    
    def build_endpoint_url(self, resource, resource_id=None):
        if resource_id:
            return f"{self.base_url}/api/{resource}/{resource_id}"
        return f"{self.base_url}/api/{resource}"
    
    def create_auth_header(self):
        return f"Bearer {self.api_key}"
    
    def log_request(self, method, endpoint, status_code):
        timestamp = datetime.datetime.now().isoformat()
        log_entry = f"[{timestamp}] {method} {endpoint} -> {status_code}"
        print(log_entry)

def process_user_data(users):
    results = []
    
    for user in users:
        # Consistent f-string usage throughout
        full_name = f"{user['first_name']} {user['last_name']}"
        display_info = f"{full_name} ({user['email']})"
        
        if user.get('age'):
            display_info = f"{display_info} - Age: {user['age']}"
        
        if user.get('premium'):
            badge = f"Premium Member since {user['premium_since']}"
            display_info = f"{display_info} - {badge}"
        
        results.append({
            'display': display_info,
            'formatted_name': full_name,
            'user_id': user['id']
        })
    
    return results

def generate_report_summary(data):
    total_users = len(data['users'])
    active_users = len([u for u in data['users'] if u['active']])
    premium_users = len([u for u in data['users'] if u.get('premium')])
    
    # Multiple f-strings with calculations
    activity_rate = (active_users / total_users) * 100 if total_users > 0 else 0
    premium_rate = (premium_users / total_users) * 100 if total_users > 0 else 0
    
    summary_lines = [
        f"Total Users: {total_users:,}",
        f"Active Users: {active_users:,} ({activity_rate:.1f}%)",
        f"Premium Users: {premium_users:,} ({premium_rate:.1f}%)",
        f"Report Generated: {datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')}"
    ]
    
    return "\n".join(summary_lines)

def format_file_info(filepath, size_bytes):
    # f-string with file size formatting
    if size_bytes >= 1024**3:
        size_str = f"{size_bytes / (1024**3):.2f} GB"
    elif size_bytes >= 1024**2:
        size_str = f"{size_bytes / (1024**2):.2f} MB"
    elif size_bytes >= 1024:
        size_str = f"{size_bytes / 1024:.2f} KB"
    else:
        size_str = f"{size_bytes} bytes"
    
    return f"File: {filepath} ({size_str})"

def create_notification_message(user_name, action, resource, timestamp):
    # f-string for notification formatting
    time_str = timestamp.strftime('%H:%M on %B %d, %Y')
    return f"Hello {user_name}, you {action} '{resource}' at {time_str}"

# f-string with complex nested expressions
def format_query_results(results, query_time_ms, total_count):
    result_count = len(results)
    
    if result_count == 0:
        return f"No results found (query took {query_time_ms:.2f}ms)"
    elif result_count == total_count:
        return f"All {result_count:,} results returned (query took {query_time_ms:.2f}ms)"
    else:
        return f"Showing {result_count:,} of {total_count:,} results (query took {query_time_ms:.2f}ms)"