#!/usr/bin/env bash

# Variables
REMOTE_HOST="ec2-user@${bastion}"
REMOTE_DIR="/home/ec2-user/my_project"
SSH_KEY="~/.ssh/personal.pem"

# Ensure the remote directory exists and set the correct permissions
ssh -i "${SSH_KEY}" "${REMOTE_HOST}" "sudo mkdir -p ${REMOTE_DIR} && sudo chown -R ec2-user:ec2-user ${REMOTE_DIR}"

# Use rsync to copy files, excluding the .git directory
rsync -avz --exclude '.git' -e "ssh -i ${SSH_KEY}" ./ "${REMOTE_HOST}:${REMOTE_DIR}"

# Run the deploy target
ssh -i "${SSH_KEY}" "${REMOTE_HOST}" "cd ${REMOTE_DIR} && make deploy"

kubectl rollout restart ds/volumescaler-daemonset