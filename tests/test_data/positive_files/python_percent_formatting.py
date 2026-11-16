# Python code using % formatting for strings (should be BLOCKED)

def create_user_profile(name, age, city):
    # Using % formatting - should trigger hook
    profile = "Name: %s, Age: %d, City: %s" % (name, age, city)
    return profile

def format_coordinates(latitude, longitude):
    # % formatting with float precision
    coords = "Coordinates: %.6f, %.6f" % (latitude, longitude)
    return coords

class FileManager:
    def __init__(self, base_path):
        self.base_path = base_path
    
    def create_file_path(self, filename, extension):
        # % formatting in class method
        full_path = "%s/%s.%s" % (self.base_path, filename, extension)
        return full_path
    
    def log_file_operation(self, operation, filename, size):
        # Multiple % formatting
        log_message = "Operation: %s | File: %s | Size: %d bytes" % (
            operation, filename, size
        )
        print(log_message)

def generate_html_tag(tag_name, content, css_class=None):
    if css_class:
        # % formatting with conditional logic
        return "<%s class='%s'>%s</%s>" % (tag_name, css_class, content, tag_name)
    else:
        return "<%s>%s</%s>" % (tag_name, content, tag_name)

# % formatting with dictionary
def format_user_data(user_dict):
    formatted = "User %(name)s (ID: %(id)d) - Email: %(email)s" % user_dict
    return formatted

def create_database_connection_string(host, port, database, user):
    # % formatting for connection string
    conn_str = "postgresql://%s@%s:%d/%s" % (user, host, port, database)
    return conn_str

# Multiple % operations in one function
def generate_email_template(recipient, subject, body, sender):
    email_header = "To: %s\nFrom: %s\nSubject: %s\n\n" % (recipient, sender, subject)
    email_body = "Dear %s,\n\n%s\n\nBest regards,\n%s" % (recipient, body, sender)
    
    return email_header + email_body