#!/bin/sh
set -eu
: "${RULE_PUBLISHER_SSH_CONFIG:?}"
exec ssh -F "$RULE_PUBLISHER_SSH_CONFIG" -T rule-publisher
