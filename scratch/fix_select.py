import re
with open('internal/web/templates/settings_users.html', 'r') as f:
    content = f.read()

content = content.replace('<select id="newAuthSource" name="auth_source" onchange="onAuthSourceChange(this.value)" required>',
'<select id="newAuthSource" name="auth_source" onchange="onAuthSourceChange(this.value)" style="width: 100%; box-sizing: border-box;" required>')

with open('internal/web/templates/settings_users.html', 'w') as f:
    f.write(content)

with open('internal/web/templates/settings_ldap.html', 'r') as f:
    content = f.read()

content = content.replace('<select id="ldapProtocol" name="ldap_protocol" onchange="onProtocolChange(this.value)">',
'<select id="ldapProtocol" name="ldap_protocol" onchange="onProtocolChange(this.value)" style="width: 100%; box-sizing: border-box;">')

with open('internal/web/templates/settings_ldap.html', 'w') as f:
    f.write(content)
