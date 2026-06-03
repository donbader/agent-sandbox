#!/bin/bash
set -e
cat > /etc/ssh/ssh_host_ed25519_key << 'HOSTKEY'
-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACDadqFFME/qjc5u3bWwHwNj2x07Ma8lRQehkzGcmkxODAAAAIgGmdFMBpnR
TAAAAAtzc2gtZWQyNTUxOQAAACDadqFFME/qjc5u3bWwHwNj2x07Ma8lRQehkzGcmkxODA
AAAECob+XxiAvtEUR8+euFec3zb7Ee4NRsLxGlJG4YFetiU9p2oUUwT+qNzm7dtbAfA2Pb
HTsxryVFB6GTMZyaTE4MAAAAAAECAwQF
-----END OPENSSH PRIVATE KEY-----
HOSTKEY
chmod 600 /etc/ssh/ssh_host_ed25519_key
ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key > /etc/ssh/ssh_host_ed25519_key.pub
mkdir -p /home/agent/.ssh
cat > /home/agent/.ssh/authorized_keys << 'PUBKEY'
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAqK8m7AKNBBjA56QBKtbZrob+LEA+26OGYTFnEO8ZpI agent-sandbox-local
PUBKEY
chown -R agent:agent /home/agent/.ssh
/usr/sbin/sshd -p 2222
