#!/bin/sh
set -eu

print_value() {
  key="$1"
  value="${2:-}"
  if [ -n "$value" ]; then
    printf '%s=%s\n' "$key" "$value"
  else
    printf '%s=<unset>\n' "$key"
  fi
}

print_value MADARI_CLOUDSQL_PROJECT "${MADARI_CLOUDSQL_PROJECT:-}"
print_value MADARI_CLOUDSQL_INSTANCE "${MADARI_CLOUDSQL_INSTANCE:-}"
print_value MADARI_CLOUDSQL_DATABASE "${MADARI_CLOUDSQL_DATABASE:-}"
print_value MADARI_CLOUDSQL_REGION "${MADARI_CLOUDSQL_REGION:-}"
print_value MADARI_CLOUDSQL_DIALECT "${MADARI_CLOUDSQL_DIALECT:-}"
