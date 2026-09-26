import re
with open('internal/web/templates/settings_ldap.html', 'r') as f:
    content = f.read()

# Fix LDAP checkbox
old_ldap = '<span style="font-weight: 600; color: #fff;">LDAP / Active Directory Authentifizierung aktivieren</span>'
new_ldap = '<span style="font-weight: 600; color: #fff; font-size: 13.5px; text-transform: none; letter-spacing: normal;">LDAP / Active Directory Authentifizierung aktivieren</span>'
content = content.replace(old_ldap, new_ldap)

# Fix TLS checkbox
old_tls = '<span style="color: #e4e4e7; font-size: 13px;">TLS-Zertifikatsprüfung überspringen (InsecureSkipVerify - nützlich für interne CAs oder selbstsignierte Zertifikate)</span>'
new_tls = '<span style="color: var(--text-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px;">TLS-Zertifikatsprüfung überspringen (InsecureSkipVerify)</span>'
content = content.replace(old_tls, new_tls)

with open('internal/web/templates/settings_ldap.html', 'w') as f:
    f.write(content)
