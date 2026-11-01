/**
 * Configuration using secure secret management practices
 * This should NOT be blocked by the hook
 */

const dotenv = require('dotenv');
const { SecretManagerServiceClient } = require('@google-cloud/secret-manager');
const AWS = require('aws-sdk');

// Good: Load environment variables from .env file
dotenv.config();

// Good: Configuration using environment variables
const config = {
    // Good: Environment variables for sensitive data
    stripe: {
        publishableKey: process.env.STRIPE_PUBLISHABLE_KEY,
        secretKey: process.env.STRIPE_SECRET_KEY
    },
    
    // Good: AWS credentials from environment or IAM roles
    aws: {
        region: process.env.AWS_REGION || 'us-east-1'
        // AWS SDK automatically picks up credentials from environment or IAM
    },
    
    // Good: Database URLs from environment
    database: {
        url: process.env.DATABASE_URL,
        mongoUrl: process.env.MONGODB_URL
    },
    
    // Good: Third-party service keys from environment
    services: {
        sendgrid: {
            apiKey: process.env.SENDGRID_API_KEY
        },
        twilio: {
            accountSid: process.env.TWILIO_ACCOUNT_SID,
            authToken: process.env.TWILIO_AUTH_TOKEN
        },
        github: {
            token: process.env.GITHUB_TOKEN
        }
    }
};

// Good: JWT secrets from environment
const JWT_SECRET = process.env.JWT_SECRET;
const REFRESH_TOKEN_SECRET = process.env.REFRESH_TOKEN_SECRET;

// Good: Encryption keys from environment
const ENCRYPTION_KEY = process.env.ENCRYPTION_KEY;
const CIPHER_KEY = process.env.CIPHER_KEY;

// Good: OAuth configuration from environment
const oauth = {
    google: {
        clientId: process.env.GOOGLE_CLIENT_ID,
        clientSecret: process.env.GOOGLE_CLIENT_SECRET
    },
    facebook: {
        appId: process.env.FACEBOOK_APP_ID,
        appSecret: process.env.FACEBOOK_APP_SECRET
    },
    twitter: {
        apiKey: process.env.TWITTER_API_KEY,
        apiSecret: process.env.TWITTER_API_SECRET,
        bearerToken: process.env.TWITTER_BEARER_TOKEN
    }
};

// Good: Webhook secrets from environment
const STRIPE_WEBHOOK_SECRET = process.env.STRIPE_WEBHOOK_SECRET;
const GITHUB_WEBHOOK_SECRET = process.env.GITHUB_WEBHOOK_SECRET;

// Good: API keys from environment
const apiKeys = {
    openai: process.env.OPENAI_API_KEY,
    anthropic: process.env.ANTHROPIC_API_KEY,
    slack: process.env.SLACK_BOT_TOKEN,
    discord: process.env.DISCORD_BOT_TOKEN
};

// Good: Session configuration from environment
const session = {
    secret: process.env.SESSION_SECRET,
    cookieSecret: process.env.COOKIE_SECRET
};

// Good: Payment processor configuration from environment
const payments = {
    paypal: {
        clientId: process.env.PAYPAL_CLIENT_ID,
        clientSecret: process.env.PAYPAL_CLIENT_SECRET
    },
    square: {
        accessToken: process.env.SQUARE_ACCESS_TOKEN,
        applicationId: process.env.SQUARE_APPLICATION_ID
    }
};

// Good: Cloud service credentials from environment
const cloud = {
    gcp: {
        projectId: process.env.GCP_PROJECT_ID,
        keyFilename: process.env.GOOGLE_APPLICATION_CREDENTIALS
    },
    azure: {
        tenantId: process.env.AZURE_TENANT_ID,
        clientId: process.env.AZURE_CLIENT_ID,
        clientSecret: process.env.AZURE_CLIENT_SECRET
    }
};

// Good: Redis configuration from environment
const redis = {
    url: process.env.REDIS_URL || 'redis://localhost:6379'
};

// Good: Function to validate required environment variables
function validateEnvironmentVariables() {
    const requiredVars = [
        'JWT_SECRET',
        'DATABASE_URL',
        'STRIPE_SECRET_KEY'
    ];
    
    const missingVars = requiredVars.filter(varName => !process.env[varName]);
    
    if (missingVars.length > 0) {
        throw new Error(`Missing required environment variables: ${missingVars.join(', ')}`);
    }
}

// Good: Function to get secrets from Google Secret Manager
async function getSecretFromGoogleSecretManager(secretName) {
    const client = new SecretManagerServiceClient();
    const projectId = process.env.GCP_PROJECT_ID;
    
    const [version] = await client.accessSecretVersion({
        name: `projects/${projectId}/secrets/${secretName}/versions/latest`
    });
    
    return version.payload.data.toString('utf8');
}

// Good: Function to get secrets from AWS Secrets Manager
async function getSecretFromAWSSecretsManager(secretName) {
    const secretsManager = new AWS.SecretsManager({
        region: process.env.AWS_REGION || 'us-east-1'
    });
    
    try {
        const data = await secretsManager.getSecretValue({ SecretId: secretName }).promise();
        return JSON.parse(data.SecretString);
    } catch (error) {
        console.error('Error retrieving secret:', error);
        throw error;
    }
}

// Good: Function to get configuration with fallbacks
function getConfigValue(key, defaultValue = null) {
    return process.env[key] || defaultValue;
}

// Good: Secure configuration initialization
async function initializeSecureConfig() {
    // Validate required environment variables
    validateEnvironmentVariables();
    
    // Example of loading additional secrets from secure stores
    try {
        if (process.env.USE_SECRET_MANAGER === 'true') {
            config.database.encryptionKey = await getSecretFromGoogleSecretManager('db-encryption-key');
            config.services.paymentGateway = await getSecretFromAWSSecretsManager('payment-gateway-config');
        }
    } catch (error) {
        console.warn('Could not load secrets from secret manager:', error.message);
    }
    
    return config;
}

// Good: Example of safe configuration patterns
const safeConfig = {
    app: {
        name: 'MyApplication',
        version: process.env.APP_VERSION || '1.0.0',
        environment: process.env.NODE_ENV || 'development',
        port: parseInt(process.env.PORT) || 3000
    },
    
    // Good: Non-sensitive configuration
    features: {
        enableNewFeature: process.env.ENABLE_NEW_FEATURE === 'true',
        maxUploadSize: parseInt(process.env.MAX_UPLOAD_SIZE) || 10485760, // 10MB
        rateLimitPerHour: parseInt(process.env.RATE_LIMIT_PER_HOUR) || 1000
    },
    
    // Good: Public configuration (not secrets)
    external: {
        apiBaseUrl: process.env.API_BASE_URL || 'https://api.example.com',
        documentationUrl: 'https://docs.example.com',
        supportEmail: 'support@example.com'
    }
};

// Good: Example placeholders and documentation strings (not real secrets)
const PLACEHOLDER_EXAMPLES = {
    stripe_key_format: 'sk_live_<example_format>',
    aws_key_format: 'AKIA<example_format>',
    github_token_format: 'ghp_<example_format>',
    webhook_secret_format: 'whsec_XXXXXXXXXXXXXXXXXXXXXXXX'
};

module.exports = {
    config,
    oauth,
    apiKeys,
    session,
    payments,
    cloud,
    redis,
    safeConfig,
    validateEnvironmentVariables,
    getSecretFromGoogleSecretManager,
    getSecretFromAWSSecretsManager,
    getConfigValue,
    initializeSecureConfig,
    PLACEHOLDER_EXAMPLES
};