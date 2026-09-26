import re
with open('internal/web/static/style.css', 'r') as f:
    content = f.read()

# Replace the input/select block to add height: 34px;
old_css = """input[type="text"], input[type="password"], input[type="email"], input[type="number"], select, textarea {
  width: 100%;
  display: block;
  box-sizing: border-box;
  background: #111111;
  border: 1px solid var(--surface-border);
  color: #ffffff;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  font-size: 13px;
  transition: border-color 0.15s ease;
  font-family: inherit;
  color-scheme: dark;
}"""

new_css = """input[type="text"], input[type="password"], input[type="email"], input[type="number"], select, textarea {
  width: 100%;
  height: 35px;
  display: block;
  box-sizing: border-box;
  background: #111111;
  border: 1px solid var(--surface-border);
  color: #ffffff;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  font-size: 13px;
  transition: border-color 0.15s ease;
  font-family: inherit;
  color-scheme: dark;
}
textarea {
  height: auto; /* textarea should not be restricted to 35px */
}"""

content = content.replace(old_css, new_css)

with open('internal/web/static/style.css', 'w') as f:
    f.write(content)
