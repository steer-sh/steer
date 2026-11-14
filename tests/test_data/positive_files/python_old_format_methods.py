# Python code using .format() method for string formatting (should be BLOCKED)

def create_user_message(username, email, age):
    # Using .format() method - should trigger hook
    message = "User {} with email {} is {} years old".format(username, email, age)
    return message

def generate_sql_query(table, field, value):
    # Another .format() usage
    query = "SELECT * FROM {} WHERE {} = '{}'".format(table, field, value)
    return query

class DatabaseLogger:
    def __init__(self, db_name):
        self.db_name = db_name
    
    def log_action(self, action, user_id, timestamp):
        # Using .format() with positional arguments
        log_entry = "Database {0}: Action '{1}' by user {2} at {3}".format(
            self.db_name, action, user_id, timestamp
        )
        print(log_entry)
    
    def log_error(self, error_code, message):
        # Using .format() with named arguments
        error_log = "Error {code} in database {db}: {msg}".format(
            code=error_code, db=self.db_name, msg=message
        )
        print(error_log)

def format_currency(amount, currency):
    # .format() with number formatting
    return "Amount: {:.2f} {}".format(amount, currency)

# Multiple .format() calls in one function
def create_report(data):
    header = "Report generated on {}".format(data['date'])
    summary = "Total records: {}, Processed: {}".format(
        data['total'], data['processed']
    )
    footer = "Report ID: {}".format(data['report_id'])
    
    return "\n".join([header, summary, footer])