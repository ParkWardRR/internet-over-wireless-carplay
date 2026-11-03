#!/bin/sh
# Pre-commit guard: refuse to commit the cars' PSKs or SSID digits (the SSID
# digits are the end of the VIN). Values come from secrets/carplay.env, which
# is gitignored. Install: ln -sf ../../scripts/check-secrets.sh .git/hooks/pre-commit
set -eu
ENV=$(git rev-parse --show-toplevel)/secrets/carplay.env
[ -f "$ENV" ] || exit 0
# only added lines: removing a secret must be allowed
staged=$(git diff --cached -U0 | grep "^+" | grep -v "^+++" || true)
bad=0
while IFS= read -r v; do
	[ -n "$v" ] || continue
	if printf "%s" "$staged" | grep -q -F -- "$v"; then
		echo "check-secrets: staged changes contain a car PSK or SSID digits (${v%"${v#???}"}***)" >&2
		bad=1
	fi
done <<EOF2
$(sed -n "s/^CAR_[AB]_PSK='\(.*\)'.*/\1/p; s/^CAR_[AB]_SSID='BMW\([0-9]*\) .*/\1/p" "$ENV")
EOF2
exit $bad
