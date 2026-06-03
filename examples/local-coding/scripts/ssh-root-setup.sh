#!/bin/bash
set -e
if [ ! -f /etc/ssh/ssh_host_ed25519_key ]; then
  ssh-keygen -t ed25519 -f /etc/ssh/ssh_host_ed25519_key -N "" -q
fi
mkdir -p /home/agent/.ssh
cp /run/secrets/authorized_keys /home/agent/.ssh/authorized_keys
chown -R agent:agent /home/agent/.ssh
chmod 600 /home/agent/.ssh/authorized_keys
/usr/sbin/sshd -p 2222
