#!/bin/sh
set -eu
umask 077

: "${RULE_PUBLISHER_BINARY:?}"
: "${RULE_PUBLISHER_STARTUP:?}"
: "${RULE_PUBLISHER_CONTROL_SOCKET:?}"
: "${RULE_PUBLISHER_KERNEL_PID:?}"
: "${RULE_PUBLISHER_LOCK:?}"
: "${RULE_PUBLISHER_BACKUPS:?}"
pid=$(cat "$RULE_PUBLISHER_KERNEL_PID")
case "$pid" in ''|*[!0-9]*) exit 1;; esac
core=$(readlink -f "/proc/$pid/exe")
test -x "$core"
exec flock -x "$RULE_PUBLISHER_LOCK" "$RULE_PUBLISHER_BINARY" \
  "$RULE_PUBLISHER_STARTUP" "$RULE_PUBLISHER_CONTROL_SOCKET" "$core" "$RULE_PUBLISHER_BACKUPS"
