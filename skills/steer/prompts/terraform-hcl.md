# Terraform HCL Hook Guidance

Use this guidance when the rule targets Terraform/HCL configuration: resource settings, tagging, security groups, provider versions.

## File Filtering

Only check `.tf` files:
```python
if not file_path.endswith('.tf'):
    sys.exit(0)
```

## Parsing Strategy: Brace Counting (NOT regex for blocks)

HCL has nested `{}` blocks. Regex with nested structures causes ReDoS. Use brace counting:

```python
def find_resource_block(content, start_index):
    """Find complete resource block using safe brace counting"""
    try:
        open_brace = content.index('{', start_index)
        brace_count = 1
        i = open_brace + 1
        while i < len(content) and brace_count > 0:
            if content[i] == '{': brace_count += 1
            elif content[i] == '}': brace_count -= 1
            i += 1
        if brace_count == 0:
            return content[open_brace:i]
    except ValueError:
        return None
    return None
```

## Common Patterns

### S3 Bucket Versioning
```python
bucket_pattern = r'resource\s+"aws_s3_bucket"\s+"([^"]+)"\s*\{'
# For each bucket, find its block and check for versioning:
versioning_pattern = r'versioning\s*\{\s*enabled\s*=\s*true'
```

### Resource Tagging
```python
resource_pattern = r'resource\s+"([^"]+)"\s+"([^"]+)"\s*\{'
required_tags = ['Environment', 'Project', 'Owner']
# Check tags block within resource for each required tag
```

### Security Group Rules
```python
# Detect overly permissive ingress rules
# SSH from 0.0.0.0/0:
ssh_open = r'from_port\s*=\s*22.*?cidr_blocks\s*=\s*\["0\.0\.0\.0/0"\]'
# RDP from 0.0.0.0/0:
rdp_open = r'from_port\s*=\s*3389.*?cidr_blocks\s*=\s*\["0\.0\.0\.0/0"\]'
```

### Taggable Resources
Not all AWS resources support tags. Common taggable resources:
`aws_instance`, `aws_s3_bucket`, `aws_vpc`, `aws_subnet`, `aws_security_group`, `aws_iam_role`, `aws_lambda_function`

## Line Number Calculation

For HCL violations, calculate line numbers from content position:
```python
line_num = content[:match_start].count('\n') + 1
```
