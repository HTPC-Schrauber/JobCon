import re
with open('internal/web/static/style.css', 'r') as f:
    content = f.read()

content = content.replace('input[type="text"], input[type="password"], input[type="email"], input[type="number"], select, textarea {\n  width: 100%;',
'input[type="text"], input[type="password"], input[type="email"], input[type="number"], select, textarea {\n  width: 100%;\n  display: block;\n  box-sizing: border-box;')

with open('internal/web/static/style.css', 'w') as f:
    f.write(content)
