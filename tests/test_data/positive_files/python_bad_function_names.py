"""
Examples of Python functions and variables with bad naming conventions
This should be blocked by the hook
"""

# Bad: function names not in snake_case
def calculateTotal(items):  # Bad: should be calculate_total
    return sum(items)

def ProcessData(data):  # Bad: should be process_data
    return data.strip()

def getUserById(user_id):  # Bad: should be get_user_by_id
    return f"User {user_id}"

def HTTPRequest(url):  # Bad: should be http_request
    return f"Requesting {url}"

# Bad: variable names not in snake_case  
userName = "john"  # Bad: should be user_name
maxRetries = 3  # Bad: should be max_retries
isValidEmail = True  # Bad: should be is_valid_email
dataBaseConnection = None  # Bad: should be database_connection

def bad_naming_example():
    # Bad: local variables with poor naming
    firstName = "Jane"  # Bad: should be first_name
    lastName = "Doe"  # Bad: should be last_name
    phoneNumber = "555-1234"  # Bad: should be phone_number
    emailAddress = "jane@example.com"  # Bad: should be email_address
    
    return firstName, lastName

# Bad: method names in classes
class UserService:
    def createUser(self, data):  # Bad: should be create_user
        pass
    
    def deleteUser(self, user_id):  # Bad: should be delete_user
        pass
    
    def updateUserProfile(self, user_id, profile):  # Bad: should be update_user_profile
        pass

# Bad: constants that should be ALL_CAPS but using other conventions
maxConnections = 100  # Bad: constants should be MAX_CONNECTIONS
defaultTimeout = 30  # Bad: should be DEFAULT_TIMEOUT

# Bad: function parameters with poor naming
def process_order(orderData, userInfo, paymentDetails):  # Bad: parameters should be snake_case
    return True

def send_email(toAddress, fromAddress, subjectLine):  # Bad: should be to_address, from_address, subject_line
    pass

# Bad: loop variables and comprehensions
def bad_loops():
    items = [1, 2, 3, 4, 5]
    
    # Bad: loop variable naming
    for itemValue in items:  # Bad: should be item_value
        print(itemValue)
    
    # Bad: comprehension variable naming
    result = [itemValue * 2 for itemValue in items]  # Bad: should be item_value
    return result

# Bad: exception handling with poor naming
def handle_errors():
    try:
        risky_operation()
    except Exception as errorMessage:  # Bad: should be error_message
        print(errorMessage)