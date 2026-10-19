"""
Examples of code properly using environment variables for secrets
This should NOT be blocked by the hook
"""

import os
import requests
import boto3
from dotenv import load_dotenv

# Good: Load environment variables
load_dotenv()

# Good: Using environment variables for sensitive data
STRIPE_SECRET_KEY = os.getenv('STRIPE_SECRET_KEY')
STRIPE_PUBLISHABLE_KEY = os.getenv('STRIPE_PUBLISHABLE_KEY')

# Good: AWS credentials from environment
AWS_ACCESS_KEY_ID = os.getenv('AWS_ACCESS_KEY_ID')
AWS_SECRET_ACCESS_KEY = os.getenv('AWS_SECRET_ACCESS_KEY')
AWS_REGION = os.getenv('AWS_DEFAULT_REGION', 'us-east-1')

# Good: GitHub token from environment
GITHUB_TOKEN = os.environ.get('GITHUB_TOKEN')

# Good: OpenAI API key from environment
OPENAI_API_KEY = os.getenv('OPENAI_API_KEY')

def process_payment():
    """Good: Getting API key from environment"""
    stripe_key = os.environ.get('STRIPE_SECRET_KEY')
    
    if not stripe_key:
        raise ValueError("STRIPE_SECRET_KEY environment variable not set")
    
    headers = {
        "Authorization": f"Bearer {stripe_key}",
        "Content-Type": "application/json"
    }
    return requests.post("https://api.stripe.com/v1/charges", headers=headers)

def upload_to_s3():
    """Good: Using environment variables for AWS credentials"""
    # AWS SDK automatically picks up credentials from environment variables
    client = boto3.client('s3', region_name=AWS_REGION)
    return client.list_buckets()

class APIClient:
    """Good: Using environment variables in class"""
    
    def __init__(self):
        # Good: All secrets from environment
        self.twitter_api_key = os.getenv('TWITTER_API_KEY')
        self.twitter_api_secret = os.getenv('TWITTER_API_SECRET')
        self.facebook_app_id = os.getenv('FACEBOOK_APP_ID')
        self.facebook_app_secret = os.getenv('FACEBOOK_APP_SECRET')
        
        # Good: Validate required environment variables
        self._validate_credentials()
        
    def _validate_credentials(self):
        """Good: Validation of environment variables"""
        required_vars = [
            'TWITTER_API_KEY', 'TWITTER_API_SECRET',
            'FACEBOOK_APP_ID', 'FACEBOOK_APP_SECRET'
        ]
        
        missing_vars = [var for var in required_vars if not os.getenv(var)]
        if missing_vars:
            raise ValueError(f"Missing environment variables: {', '.join(missing_vars)}")
    
    def authenticate_github(self):
        """Good: GitHub token from environment"""
        token = os.getenv('GITHUB_TOKEN')
        if not token:
            raise ValueError("GITHUB_TOKEN not found in environment")
            
        headers = {"Authorization": f"token {token}"}
        return requests.get("https://api.github.com/user", headers=headers)

# Good: Database URL from environment with fallback
DATABASE_URL = os.getenv('DATABASE_URL', 'sqlite:///default.db')
REDIS_URL = os.getenv('REDIS_URL', 'redis://localhost:6379/0')

# Good: JWT secret from environment
JWT_SECRET = os.getenv('JWT_SECRET')
if not JWT_SECRET:
    raise ValueError("JWT_SECRET environment variable is required")

# Good: Encryption key from environment
ENCRYPTION_KEY = os.getenv('ENCRYPTION_KEY')
SALT = os.getenv('ENCRYPTION_SALT')

# Good: Third-party service keys from environment
SENDGRID_API_KEY = os.getenv('SENDGRID_API_KEY')
TWILIO_ACCOUNT_SID = os.getenv('TWILIO_ACCOUNT_SID')
TWILIO_AUTH_TOKEN = os.getenv('TWILIO_AUTH_TOKEN')

# Good: Webhook secret from environment
WEBHOOK_SECRET = os.getenv('WEBHOOK_SECRET')

def make_api_calls():
    """Good: All secrets from environment variables"""
    
    # Good: Slack webhook URL from environment
    slack_webhook = os.getenv('SLACK_WEBHOOK_URL')
    
    # Good: Discord bot token from environment
    discord_token = os.getenv('DISCORD_BOT_TOKEN')
    
    # Good: Weather API key from environment
    weather_key = os.getenv('WEATHER_API_KEY')
    weather_url = f"http://api.weather.com/v1/current?key={weather_key}&location=NYC"
    
    return {
        "slack": slack_webhook,
        "discord": discord_token,
        "weather": weather_url
    }

# Good: Configuration class using environment variables
class Config:
    """Good: Configuration class with environment variables"""
    
    def __init__(self):
        self.debug = os.getenv('DEBUG', 'False').lower() == 'true'
        self.secret_key = os.getenv('SECRET_KEY')
        self.database_url = os.getenv('DATABASE_URL')
        
        # Good: Validate critical configuration
        if not self.secret_key:
            raise ValueError("SECRET_KEY environment variable is required")
    
    @property
    def stripe_keys(self):
        """Good: Property method for Stripe configuration"""
        return {
            'publishable_key': os.getenv('STRIPE_PUBLISHABLE_KEY'),
            'secret_key': os.getenv('STRIPE_SECRET_KEY')
        }
    
    def get_oauth_config(self, provider):
        """Good: Dynamic OAuth configuration from environment"""
        configs = {
            'google': {
                'client_id': os.getenv('GOOGLE_CLIENT_ID'),
                'client_secret': os.getenv('GOOGLE_CLIENT_SECRET')
            },
            'github': {
                'client_id': os.getenv('GITHUB_CLIENT_ID'),
                'client_secret': os.getenv('GITHUB_CLIENT_SECRET')
            }
        }
        return configs.get(provider, {})

# Good: Using environment variables with defaults and type conversion
def get_config_value(key, default=None, value_type=str):
    """Good: Helper function for getting typed environment variables"""
    value = os.getenv(key, default)
    if value is None:
        return None
    
    if value_type == bool:
        return value.lower() in ('true', '1', 'yes', 'on')
    elif value_type == int:
        return int(value)
    elif value_type == float:
        return float(value)
    else:
        return value

# Good: Example usage of the helper function
MAX_CONNECTIONS = get_config_value('MAX_CONNECTIONS', '10', int)
ENABLE_DEBUG = get_config_value('DEBUG', 'false', bool)
API_TIMEOUT = get_config_value('API_TIMEOUT', '30.0', float)

# Good: Safe example strings (not actual secrets)
EXAMPLE_API_KEY_FORMAT = "sk_live_<example_format>"
PLACEHOLDER_TOKEN = "your-token-here"
DOCUMENTATION_EXAMPLE = "Replace 'your-api-key' with your actual API key"