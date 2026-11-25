/**
 * Configuration file with exposed secrets and API keys
 * This should be blocked by the hook
 */

// Bad: Hardcoded API keys in configuration
const config = {
    // Bad: Stripe keys
    stripe: {
        publishableKey: 'pk_live_51234567890abcdef1234567890abcdef12345678',
        secretKey: 'sk_live_51234567890abcdef1234567890abcdef12345678'
    },
    
    // Bad: AWS credentials
    aws: {
        accessKeyId: 'AKIAIOSFODNN7EXAMPLE',
        secretAccessKey: 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY',
        region: 'us-east-1'
    },
    
    // Bad: Database connection strings with embedded credentials
    database: {
        url: 'mongodb://admin:password123@cluster0.mongodb.net/myapp',
        postgresUrl: 'postgresql://user:secretpassword@localhost:5432/appdb'
    },
    
    // Bad: Third-party service keys
    services: {
        sendgrid: {
            apiKey: 'SG.1234567890abcdefghijklmnopqrstuvwxyz'
        },
        twilio: {
            accountSid: 'ACabcdefghijklmnopqrstuvwxyz123456',
            authToken: 'abcdefghijklmnopqrstuvwxyz123456'
        },
        github: {
            token: 'ghp_1234567890abcdefghijklmnopqrstuvwxyz123'
        }
    }
};

// Bad: JWT secrets
const JWT_SECRET = 'your-super-secret-jwt-key-here-make-it-complex';
const REFRESH_TOKEN_SECRET = 'another-secret-key-for-refresh-tokens-123456';

// Bad: Encryption keys
const ENCRYPTION_KEY = 'abcdefghijklmnopqrstuvwxyz123456789012345678901234567890';
const CIPHER_KEY = '1234567890abcdef1234567890abcdef';

// Bad: OAuth secrets
const oauth = {
    google: {
        clientId: '123456789012-abcdefghijklmnopqrstuvwxyz123456.apps.googleusercontent.com',
        clientSecret: 'GOCSPX-abcdefghijklmnopqrstuvwxyz123'
    },
    facebook: {
        appId: '123456789012345',
        appSecret: 'abcdefghijklmnopqrstuvwxyz123456'
    },
    twitter: {
        apiKey: 'abcdefghijklmnopqrstuvwx',
        apiSecret: 'abcdefghijklmnopqrstuvwxyz1234567890abcdefghijklmn',
        bearerToken: 'AAAAAAAAAAAAAAAAAAAAABcdefghijklmnopqrstuvwxyz1234567890abcdefghijklmnopqrstuvwxyz'
    }
};

// Bad: Webhook secrets
const STRIPE_WEBHOOK_SECRET = 'whsec_1234567890abcdefghijklmnopqrstuvwxyz';
const GITHUB_WEBHOOK_SECRET = 'webhook_secret_key_12345';

// Bad: API keys for various services
const apiKeys = {
    openai: 'sk-1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL',
    anthropic: 'sk-ant-api03-1234567890abcdefghijklmnopqrstuvwxyz',
    slack: 'xoxb-123456789012-1234567890123-abcdefghijklmnopqrstuvwx',
    discord: 'ODk4Mjk4NTI0OTAyNzI2NjU2.GZ7kDF.abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL'
};

// Bad: Session and cookie secrets
const session = {
    secret: 'keyboard_cat_secret_session_key',
    cookieSecret: 'super_secret_cookie_encryption_key_123'
};

// Bad: Payment processor keys
const payments = {
    paypal: {
        clientId: 'AQkquBDf1zctJOWGKWUEtKXm6qVhueUEMvXO_-MCI4DQEiGp62UXHjeE-w2dGwPnoDI6TRWrJYHYNPdE',
        clientSecret: 'EDqP2H6AY1Gp6YOEtKXm6qVhueUEMvXO_-MCI4DQEiGp62UXHjeE-w2dGwPnoDI6TR'
    },
    square: {
        accessToken: 'sq0atp-abcdefghijklmnopqrstuvwxyz123456',
        applicationId: 'sq0idp-abcdefghijklmnopqrstuvwxyz123456'
    }
};

// Bad: Cloud service credentials
const cloud = {
    gcp: {
        projectId: 'my-project-123456',
        keyFile: 'path/to/service-account-key.json',
        // Bad: Inline service account key
        serviceAccountKey: {
            "type": "service_account",
            "project_id": "my-project-123456",
            "private_key_id": "abcdefghijklmnopqrstuvwxyz123456",
            "private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKB\n-----END PRIVATE KEY-----\n",
            "client_email": "service-account@my-project-123456.iam.gserviceaccount.com",
            "client_id": "123456789012345678901"
        }
    },
    azure: {
        tenantId: 'abcdefgh-1234-5678-9012-abcdefghijkl',
        clientId: '12345678-abcd-efgh-ijkl-123456789012',
        clientSecret: 'abcdefghijklmnopqrstuvwxyz123456~'
    }
};

// Bad: Redis passwords
const redis = {
    password: 'redis_password_123456',
    url: 'redis://user:password123@redis-server:6379'
};

module.exports = {
    config,
    JWT_SECRET,
    REFRESH_TOKEN_SECRET,
    ENCRYPTION_KEY,
    oauth,
    apiKeys,
    session,
    payments,
    cloud,
    redis
};