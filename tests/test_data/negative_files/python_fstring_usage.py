# Python code using modern f-string formatting (should be ALLOWED)

def create_user_message(username, email, age):
    # Using f-strings - this should be allowed
    message = f"User {username} with email {email} is {age} years old"
    return message

def generate_query_safely(table, field, value):
    # f-string usage (note: this is just for string formatting demo, not actual SQL)
    query = f"SELECT * FROM {table} WHERE {field} = '{value}'"
    return query

class DatabaseLogger:
    def __init__(self, db_name):
        self.db_name = db_name
    
    def log_action(self, action, user_id, timestamp):
        # f-string in class method
        log_entry = f"Database {self.db_name}: Action '{action}' by user {user_id} at {timestamp}"
        print(log_entry)
    
    def log_error(self, error_code, message):
        # f-string with expressions
        error_log = f"Error {error_code} in database {self.db_name}: {message}"
        print(error_log)

def format_currency(amount, currency):
    # f-string with number formatting
    return f"Amount: {amount:.2f} {currency}"

def create_file_path(base_path, filename, extension):
    # Simple f-string concatenation
    full_path = f"{base_path}/{filename}.{extension}"
    return full_path

def format_coordinates(latitude, longitude):
    # f-string with float precision
    coords = f"Coordinates: {latitude:.6f}, {longitude:.6f}"
    return coords

def generate_html_tag(tag_name, content, css_class=None):
    if css_class:
        # f-string with conditional logic
        return f"<{tag_name} class='{css_class}'>{content}</{tag_name}>"
    else:
        return f"<{tag_name}>{content}</{tag_name}>"

def create_user_profile(name, age, city):
    # Multiple f-string usages
    profile = f"Name: {name}, Age: {age}, City: {city}"
    details = f"User {name} lives in {city} and is {age} years old"
    
    return f"{profile}\n{details}"

# f-string with complex expressions
def format_performance_data(data):
    avg_time = sum(data['times']) / len(data['times'])
    result = f"Performance Report: {data['name']} - Average: {avg_time:.3f}ms ({len(data['times'])} samples)"
    return result

def create_database_connection_string(host, port, database, user):
    # f-string for connection string
    conn_str = f"postgresql://{user}@{host}:{port}/{database}"
    return conn_str