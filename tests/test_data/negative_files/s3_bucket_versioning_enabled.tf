# Terraform configuration with S3 bucket versioning properly enabled
# This should NOT be blocked by the hook

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# Good: S3 bucket with versioning enabled
resource "aws_s3_bucket" "secure_data" {
  bucket = "secure-company-data-bucket"

  versioning {
    enabled = true  # Good: versioning is enabled
  }

  tags = {
    Environment = "Production"
    Security    = "High"
  }
}

# Good: Another bucket with proper versioning
resource "aws_s3_bucket" "user_content" {
  bucket = "user-generated-content"
  
  versioning {
    enabled = true     # Good: versioning enabled
    mfa_delete = true  # Additional security
  }

  lifecycle_rule {
    id      = "manage_versions"
    enabled = true

    noncurrent_version_expiration {
      days = 90
    }
  }

  tags = {
    Purpose = "UserContent"
    Backup  = "Versioned"
  }
}

# Good: Bucket with versioning and other configurations
resource "aws_s3_bucket" "config_backup" {
  bucket = "configuration-backup-bucket"
  
  versioning {
    enabled = true  # Good: properly enabled
  }

  server_side_encryption_configuration {
    rule {
      apply_server_side_encryption_by_default {
        sse_algorithm = "AES256"
      }
    }
  }

  tags = {
    Type = "ConfigBackup"
  }
}

# Other AWS resources (should not trigger the hook)
resource "aws_s3_bucket_policy" "secure_data_policy" {
  bucket = aws_s3_bucket.secure_data.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Deny"
        Principal = "*"
        Action = "s3:*"
        Resource = [
          aws_s3_bucket.secure_data.arn,
          "${aws_s3_bucket.secure_data.arn}/*",
        ]
        Condition = {
          Bool = {
            "aws:SecureTransport" = "false"
          }
        }
      },
    ]
  })
}

# Non-S3 resources should not be affected
resource "aws_iam_role" "s3_access_role" {
  name = "s3-access-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
      },
    ]
  })
}