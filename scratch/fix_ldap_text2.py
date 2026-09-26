import re
with open('internal/web/templates/settings_ldap.html', 'r') as f:
    content = f.read()

old_tls = '<span style="color: var(--text-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px;">TLS-Zertifikatsprüfung überspringen (InsecureSkipVerify)</span>'
new_tls = '<span style="color: var(--text-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px;">TLS-Zertifikatsprüfung überspringen (InsecureSkipVerify - nützlich für interne CAs oder selbstsignierte Zertifikate)</span>'
content = content.replace(old_tls, new_tls)

with open('internal/web/templates/settings_ldap.html', 'w') as f:
    f.write(content)
