# Terraform configuration with S3 bucket lacking versioning
# This should be blocked by the hook

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# Bad: S3 bucket without any versioning configuration
resource "aws_s3_bucket" "app_data" {
  bucket = "my-app-data-bucket-2024"

  tags = {
    Name        = "AppData"
    Environment = "Production"
  }
}

# Bad: Another bucket without versioning
resource "aws_s3_bucket" "user_uploads" {
  bucket = "user-uploads-bucket"
  
  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Purpose = "UserUploads"
  }
}

# Other AWS resources (should not trigger the hook)
resource "aws_s3_bucket_policy" "app_data_policy" {
  bucket = aws_s3_bucket.app_data.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "DenyInsecureConnections"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource = [
          aws_s3_bucket.app_data.arn,
          "${aws_s3_bucket.app_data.arn}/*",
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

# Bad: Third bucket also missing versioning
resource "aws_s3_bucket" "backup_storage" {
  bucket = "backup-storage-bucket"
  
  tags = {
    Type = "Backup"
  }
}