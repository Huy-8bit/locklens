#!/usr/bin/env bash
# Create deploy/.env for `make up` with random secrets, once. An existing file
# is never changed. The administrator password is printed only when created.
set -euo pipefail
cd "$(dirname "$0")/.."
env_file=deploy/.env
[[ -f "$env_file" ]] && exit 0

rand() { openssl rand -base64 "$1" | tr -d '\n'; }
password=$(openssl rand -base64 24 | tr -d '\n/+=' | cut -c1-24)
umask 077
sed -e "s|^LOCKLENS_STORE_PASSWORD=.*|LOCKLENS_STORE_PASSWORD=$(openssl rand -hex 24)|" \
    -e "s|^LOCKLENS_MASTER_KEY=.*|LOCKLENS_MASTER_KEY=$(rand 32)|" \
    -e "s|^LOCKLENS_ADMIN_PASSWORD=.*|LOCKLENS_ADMIN_PASSWORD=$password|" \
    deploy/.env.example >"$env_file"
echo "Created $env_file with random secrets (mode 600, ignored by git)."
echo "Sign in at http://127.0.0.1:8080 as admin with password: $password"
echo "The password is also in $env_file. Keep LOCKLENS_MASTER_KEY: it encrypts stored connection strings."
