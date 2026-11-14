# Documentation with Broken Image Links

This markdown file contains image references that point to non-existent files.

## Section 1: Missing Images

Here's our company logo:
![Company Logo](../images/missing_logo.png)

This is supposed to be a screenshot of our application:
![App Screenshot](../images/nonexistent_screenshot.jpg)

## Section 2: More Broken References

The following diagram shows our architecture:
![System Architecture](../images/architecture_diagram_that_does_not_exist.svg)

Here's a photo from our team meeting:
![Team Photo](../images/team_photo_missing.png)

## Section 3: Absolutely Broken Paths

This image path is completely wrong:
![Wrong Path](/completely/wrong/path/image.png)

Another broken reference:
![Broken](../../../some/deep/path/that/does/not/exist.jpg)

## Section 4: Mixed Content

Some text content here.

![Another Missing Image](images/also_missing.gif)

And here's a reference to a file that doesn't exist in the current directory:
![Local Missing](./missing_local_image.png)

## Conclusion

All the images in this document are broken and should trigger the hook to block this content.