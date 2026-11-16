# Terraform configuration with multiple S3 buckets all properly versioned
# This should NOT be blocked by the hook

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# Good: First bucket with versioning
resource "aws_s3_bucket" "primary_storage" {
  bucket = "primary-data-storage"

  versioning {
    enabled = true  # Good: versioning enabled
  }

  tags = {
    Tier = "Primary"
  }
}

# Good: Second bucket with versioning
resource "aws_s3_bucket" "secondary_storage" {
  bucket = "secondary-data-storage"
  
  versioning {
    enabled = true  # Good: versioning enabled
  }

  tags = {
    Tier = "Secondary"
  }
}

# Good: Third bucket with versioning and MFA delete
resource "aws_s3_bucket" "critical_backups" {
  bucket = "critical-system-backups"
  
  versioning {
    enabled    = true  # Good: versioning enabled
    mfa_delete = true  # Additional protection
  }

  tags = {
    Critical = "true"
    Backup   = "System"
  }
}

# Good: Fourth bucket for logs with versioning
resource "aws_s3_bucket" "audit_logs" {
  bucket = "security-audit-logs"
  
  versioning {
    enabled = true  # Good: versioning enabled
  }

  lifecycle_rule {
    id      = "audit_log_lifecycle"
    enabled = true

    transition {
      days          = 30
      storage_class = "STANDARD_IA"
    }

    transition {
      days          = 90
      storage_class = "GLACIER"
    }

    noncurrent_version_transition {
      days          = 30
      storage_class = "GLACIER"
    }

    noncurrent_version_expiration {
      days = 365
    }
  }

  tags = {
    Purpose = "AuditLogs"
    Retention = "1Year"
  }
}

# Good: Fifth bucket for media with versioning
resource "aws_s3_bucket" "media_assets" {
  bucket = "company-media-assets"
  
  versioning {
    enabled = true  # Good: versioning enabled
  }

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = ["https://company.com"]
    expose_headers  = ["ETag"]
    max_age_seconds = 3000
  }

  tags = {
    ContentType = "Media"
    Public = "false"
  }
}

# Supporting resources that don't need versioning checks
resource "aws_s3_bucket_notification" "primary_notification" {
  bucket = aws_s3_bucket.primary_storage.id

  lambda_function {
    lambda_function_arn = "arn:aws:lambda:us-east-1:123456789012:function:ProcessUploads"
    events              = ["s3:ObjectCreated:*"]
  }
}