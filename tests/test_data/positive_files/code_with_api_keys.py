"""
Examples of code with hardcoded API keys and secrets
This should be blocked by the hook
"""

import requests
import boto3

# Bad: Hardcoded Stripe API keys
STRIPE_SECRET_KEY = "sk_live_51234567890abcdef1234567890abcdef12345678"
STRIPE_PUBLISHABLE_KEY = "pk_live_51234567890abcdef1234567890abcdef12345678"

# Bad: Hardcoded AWS credentials
AWS_ACCESS_KEY_ID = "AKIAIOSFODNN7EXAMPLE"
AWS_SECRET_ACCESS_KEY = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

# Bad: Hardcoded GitHub token
GITHUB_TOKEN = "ghp_1234567890abcdefghijklmnopqrstuvwxyz123"

# Bad: Hardcoded OpenAI API key
OPENAI_API_KEY = "sk-1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"

def process_payment():
    """Bad: Using hardcoded API key"""
    stripe_key = "sk_live_abcdefghijklmnopqrstuvwxyz123456"
    
    # Make API call with hardcoded key
    headers = {
        "Authorization": f"Bearer {stripe_key}",
        "Content-Type": "application/json"
    }
    return requests.post("https://api.stripe.com/v1/charges", headers=headers)

def upload_to_s3():
    """Bad: Hardcoded AWS credentials in function"""
    client = boto3.client(
        's3',
        aws_access_key_id='AKIAI44QH8DHBEXAMPLE',
        aws_secret_access_key='je7MtGbClwBF/2Zp9Utk/h3yCo8nvbEXAMPLEKEY'
    )
    return client.list_buckets()

class APIClient:
    """Bad: Hardcoded secrets in class"""
    
    def __init__(self):
        # Bad: Various hardcoded API keys
        self.twitter_api_key = "1234567890abcdefghijklmnopqrstuvwxyz"
        self.twitter_api_secret = "abcdefghijklmnopqrstuvwxyz1234567890"
        self.facebook_app_id = "123456789012345"
        self.facebook_app_secret = "abcdefghijklmnopqrstuvwxyz123456"
        
    def authenticate_github(self):
        """Bad: Hardcoded GitHub personal access token"""
        token = "github_pat_11ABCDEFGH0123456789_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
        headers = {"Authorization": f"token {token}"}
        return requests.get("https://api.github.com/user", headers=headers)

# Bad: Hardcoded database credentials
DATABASE_URL = "postgresql://username:password123@localhost:5432/mydb"
REDIS_URL = "redis://user:password456@localhost:6379/0"

# Bad: Hardcoded JWT secret
JWT_SECRET = "your-256-bit-secret-key-here-make-it-long-and-random"

# Bad: Hardcoded encryption keys
ENCRYPTION_KEY = "abcdefghijklmnopqrstuvwxyz123456"
SALT = "randomsalt123"

# Bad: Hardcoded third-party service keys
SENDGRID_API_KEY = "SG.1234567890abcdefghijklmnopqrstuvwxyz"
TWILIO_ACCOUNT_SID = "ACabcdefghijklmnopqrstuvwxyz123456"
TWILIO_AUTH_TOKEN = "abcdefghijklmnopqrstuvwxyz123456"

# Bad: Hardcoded webhook secrets
WEBHOOK_SECRET = "whsec_1234567890abcdefghijklmnopqrstuvwxyz"

def make_api_calls():
    """Bad: Multiple hardcoded secrets in one function"""
    
    # Bad: Slack webhook URL with token
    slack_webhook = "https://hooks.slack.com/services/T12345678/B12345678/abcdefghijklmnopqrstuvwx"
    
    # Bad: Discord bot token
    discord_token = "ODk4Mjk4NTI0OTAyNzI2NjU2.GZ7kDF.abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"
    
    # Bad: API key in URL
    weather_url = "http://api.weather.com/v1/current?key=abcdef123456789&location=NYC"
    
    return {
        "slack": slack_webhook,
        "discord": discord_token,
        "weather": weather_url
    }

# Bad: Hardcoded passwords and tokens in different formats
passwords = {
    "admin_password": "SuperSecret123!",
    "api_token": "1234567890abcdefghijklmnopqrstuvwxyz",
    "session_secret": "keyboard_cat_secret_key_12345"
}