#!/bin/bash
set -e
cp /run/ssh/host_key /etc/ssh/ssh_host_ed25519_key
chmod 600 /etc/ssh/ssh_host_ed25519_key
ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key > /etc/ssh/ssh_host_ed25519_key.pub
mkdir -p /home/agent/.ssh
cp /run/ssh/authorized_keys /home/agent/.ssh/authorized_keys
chown -R agent:agent /home/agent/.ssh
/usr/sbin/sshd -p 2222
