function addCopyButtonToCodeBlocks() {
    // Don't run the script on the main landing page
    if (window.location.pathname === "/funnel/" || window.location.pathname === "/") {
        return;
    }

    // Get all <pre> elements
    const preElements = document.querySelectorAll('pre');
    const copyIcon = '⧉';
    const copiedIcon = 'Copied ✓';

    // For each <pre> element, add a copy button inside a header
    preElements.forEach((preElement) => {
        // Get the first child <code> element if it exists
        const codeBlock = preElement.querySelector('code');
        if (!codeBlock) {
            return; // Skip if there is no <code> block
        }

        // Create the header div
        const header = document.createElement("div");
        header.classList.add("code-block-header");

        // Create the copy button
        const copyButton = document.createElement("button");
        copyButton.classList.add("btn", "copy-code-button");
        copyButton.innerHTML = copyIcon;
        copyButton.title = "Copy";

        // Add a click event listener to the copy button
        copyButton.addEventListener("click", () => {
            // Copy the code inside the code block to the clipboard
            const codeToCopy = codeBlock.innerText;
            navigator.clipboard.writeText(codeToCopy);

            // Update the copy button text to indicate that the code has been copied
            copyButton.innerHTML = copiedIcon;
            setTimeout(() => {
                copyButton.innerHTML = copyIcon;
            }, 1500);
        });

        // Get the language from the class, if present
        const classList = Array.from(codeBlock.classList);
        const languageClass = classList.find((cls) => cls.startsWith("language-"));
        let language = languageClass ? languageClass.replace("language-", "") : "";
        if (language === "sh") {
            language = "shell";
        }

        // Create the language label
        const languageLabel = document.createElement("span");
        languageLabel.textContent = language ? language.toLowerCase() : "";
        languageLabel.style.marginRight = "10px";
        languageLabel.style.marginRight = "10px";
        languageLabel.style.fontStyle = "italic";

        // Append the language label and copy button to the header
        header.appendChild(languageLabel);
        header.appendChild(copyButton);

        // Insert the header before the <pre> element
        preElement.parentNode.insertBefore(header, preElement);
    });
}

// Call the function to add copy buttons to code blocks
document.addEventListener("DOMContentLoaded", addCopyButtonToCodeBlocks);
