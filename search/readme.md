# Search Index

## Search Term Normalization

The user input search term undergoes normalization:
1. Trim space
2. Lowercase
3. Remove invalid UTF-8 characters
4. Detect and remove quotes in the form '" (activates exact search mode)

Wildcards are not supported.

## Generic Text Normalization

1. Trim space
2. Lowercase
3. Remove invalid UTF-8 characters
