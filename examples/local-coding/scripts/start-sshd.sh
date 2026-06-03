#!/bin/sh
# Fix SSH directory permissions (sshd is already started as root in entrypoint)
mkdir -p /home/agent/.ssh
chmod 700 /home/agent/.ssh
chmod 600 /home/agent/.ssh/authorized_keys
