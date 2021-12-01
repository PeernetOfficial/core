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
The plan for the index search is the following:
```
key                       |          value
<Ex: Filename String>     |         <Filename hash>
                          |
<Generate Index hash #1>  |         <Filename hash>
<Generate Index hash #2>  |         <Filename hash>
<Generate Index hash #n>  |         <Filename hash>
```
