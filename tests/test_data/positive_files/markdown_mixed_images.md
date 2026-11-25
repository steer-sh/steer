# Documentation with Mixed Image Links

This markdown file has a mix of valid and invalid image references.

## Section 1: Valid Images

Here's our company logo (this exists):
![Company Logo](../images/logo.png)

A valid screenshot:
![Screenshot](../images/screenshot.jpg)

## Section 2: Broken Images

This one doesn't exist:
![Missing Logo](../images/missing_company_logo.png)

Neither does this one:
![Broken Screenshot](../images/broken_screenshot.jpg)

## Section 3: More Valid Images  

A valid diagram:
![Diagram](../images/diagram.svg)

## Section 4: More Broken Images

This path is wrong:
![Wrong Path](../images/does_not_exist.png)

So is this one:
![Another Wrong Path](./local_missing_file.jpg)

## Section 5: Mix of Everything

Valid reference:
![Valid Logo](../images/logo.png)

Invalid reference right after:
![Invalid Image](../images/this_file_does_not_exist.png)

Another valid one:
![Valid Diagram](../images/diagram.svg)

And another invalid one:
![Invalid Screenshot](../images/missing_screenshot.gif)

## Conclusion

This file has both valid and invalid image references, so it should be blocked due to the invalid ones.