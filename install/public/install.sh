#!/bin/sh
  set -e
  # this script installs paystable on your machine
  #
  # usage:
  #   curl -sSL https://paystable.vercel.app | sh
  #
  # it creates a directory 'paystable' in the current working directory,
  # downloads the latest compiled binary, runs `paystable init` to write
  # a local .env with generated secrets, and writes instructions.md.
  #
  # supported platforms:
  #   - linux/amd64
  #   - linux/arm64
  #   - darwin/amd64 (macOS intel)
  #   - darwin/arm64 (macOS apple silicon)
  #
  # source: https://github.com/samithreddychinni/paystable
  #

  REPO="samithreddychinni/paystable"
  BINARY="paystable"

  info() {
    echo "[INFO] $*"
  }

  error() {
    echo "[ERROR] $*" >&2
  }

  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)

  case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) error "unsupported architecture: $ARCH"; exit 1 ;;
  esac
  
  case "$OS" in
    linux|darwin) ;;
    *) error "unsupported OS: $OS"; exit 1 ;;
  esac
  
  ASSET="${BINARY}-${OS}-${ARCH}"
  
  info "starting paystable installation"
  info "detected platform: ${OS}/${ARCH}"
  
  info "fetching latest release metadata"
  LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
  grep '"tag_name"' | cut -d'"' -f4)
  if [ -z "$LATEST" ]; then
    error "could not fetch latest release"
    exit 1
  fi
  
  info "latest release: ${LATEST}"
  info "creating paystable directory"
  mkdir -p paystable
  cd paystable
  
  info "downloading ${ASSET}"
  URL="https://github.com/${REPO}/releases/download/${LATEST}/${ASSET}"
  curl -fsSL "$URL" -o "${BINARY}"
  
  info "downloading checksums"
  curl -fsSL "https://github.com/${REPO}/releases/download/${LATEST}/checksums.txt" -o checksums.txt
  
  EXPECTED=$(grep " ${ASSET}$" checksums.txt | awk '{print $1}')
  if [ -z "$EXPECTED" ]; then
    error "checksum for ${ASSET} was not found"
    exit 1
  fi
  
  info "verifying checksum"
  if command -v sha256sum >/dev/null 2>&1; then
    echo "${EXPECTED}  ${BINARY}" | sha256sum -c - >/dev/null
  elif command -v shasum >/dev/null 2>&1; then
    echo "${EXPECTED}  ${BINARY}" | shasum -a 256 -c - >/dev/null
  else
    error "sha256sum or shasum is required to verify the download"
    exit 1
  fi
  
  info "marking binary executable"
  chmod +x "${BINARY}"

  info "generating local .env"
  ./"${BINARY}" init


  info "writing instructions.md"
  cat << 'EOF' > instructions.md
# Paystable quickstart

I built Paystable to check gateway evidence before a merchant fulfills an order.
PayU is the only supported gateway in this release.

**Warning:** Do not expose the dashboard to the internet.
Admin routes have no login.

The installer ran `./paystable init` to create `.env` with local secrets.
Do not commit `.env`.

1. Create a PostgreSQL user and database.
   Use the same password in PostgreSQL and `DATABASE_URL`.

   ```sql
   CREATE USER paystable WITH PASSWORD 'CHANGE_ME';
   CREATE DATABASE paystable OWNER paystable;
   ```

2. Edit `.env`.
   Set `DATABASE_URL` to match the database.
   Set `GATEWAY_API_KEY` to the PayU test merchant key.
   Set `WEBHOOK_SECRET` to the PayU test salt.
   Set `PAYU_STATUS_URL` to the PayU test status endpoint.
   Keep the generated callback, admin, and encryption secrets private.

3. Check the local configuration and database.

   ```bash
   ./paystable doctor
   ```

   The command applies pending migrations.
   Missing gateway credentials produce warnings.
   A successful database check does not prove that PayU access works.

4. Start Paystable after you set the required values.

   ```bash
   ./paystable
   ```

5. Open the local dashboard at `http://localhost:8080/dashboard`.

Read the integration contract at https://github.com/samithreddychinni/paystable.

EOF

info "paystable ${LATEST} installed successfully"
info "next step: cd paystable && configure Postgres + PayU fields in .env"
info "then run: ./paystable doctor"
info "then start: ./paystable"
