# Terraform configuration with S3 bucket versioning explicitly disabled
# This should be blocked by the hook

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# Bad: S3 bucket with versioning block but disabled
resource "aws_s3_bucket" "documents" {
  bucket = "company-documents-bucket"

  versioning {
    enabled = false  # Bad: versioning is disabled
  }

  tags = {
    Department = "Legal"
    Compliance = "Required"
  }
}

# Bad: Another bucket with versioning disabled
resource "aws_s3_bucket" "logs" {
  bucket = "application-logs-bucket"
  
  versioning {
    enabled = false  # Bad: should be true
  }

  lifecycle_rule {
    id      = "delete_old_logs"
    enabled = true

    expiration {
      days = 90
    }
  }

  tags = {
    Purpose = "Logging"
  }
}

# Bad: Bucket with versioning suspended (also not enabled = true)
resource "aws_s3_bucket" "assets" {
  bucket = "static-assets-bucket"
  
  versioning {
    enabled = false
    mfa_delete = false  # Bad: versioning not enabled
  }

  website {
    index_document = "index.html"
    error_document = "error.html"
  }

  tags = {
    ContentType = "Static"
  }
}

# Other resources that should not trigger
resource "aws_s3_bucket_public_access_block" "documents_pab" {
  bucket = aws_s3_bucket.documents.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}