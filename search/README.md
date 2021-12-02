# Core Search

This package focuses on implementing the search feature. The current plan is
implement simple search features such as:
- Lowercase input
- Break input by ., whitespace etc.
- Minimum 3 chars
- Generating hash by word

Something useful we could consider implementing could be:
- Top-k Approximate String Matching algorithm

## Index database
For the Index database we are using a Sqlite database:
Database Structure: 
```
CREATE TABLE `search_indices` (`id` text,`hash` text,`key_hash` text,PRIMARY KEY (`id`));
```

## Features Implemented: 
- Create set of hashes based on simple characteristics such as:
  1. Basic normalization and sanitation 
  2. Upper Case 
  3. Lower Case 
  4. Individual words 

- Search through local indexes based on a text 
- Delete key string generated hashes in the indexes Sqlite database 

### Basic normalization and sanitation
This implementation does the basics such as:
  1. Ensures the string has no double space 
  2. replaces _ and - with a space 
  3. removes diacritics

Ex:
```go
NormalizeWords("français")
NormalizeWords("testé-lol_What to do-idk")

// result
francais
teste lol What to do idk
```


