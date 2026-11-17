#!/bin/sh
# Publish wiki/ to the GitHub wiki.
#
#   scripts/wiki-sync.sh          # check, then push
#   scripts/wiki-sync.sh --check  # scrub check only, change nothing
#
# The wiki source lives in wiki/ in this repository rather than only in the
# wiki's own git repo. A GitHub wiki cannot be created by pushing -- the first
# page has to be made in the web UI -- so a wiki drafted in a scratch directory
# while waiting for that is a wiki that can be lost. This one was, once.
#
# Everything here is PUBLIC, so the scrub check runs first and refuses to push
# on a hit. This repository has published secrets twice; the check is not
# optional.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
SRC=$ROOT/wiki
WIKI=${WIKI_REMOTE:-https://github.com/ParkWardRR/internet-over-wireless-carplay.wiki.git}
TMP=${TMPDIR:-/tmp}/iowc-wiki-sync.$$

die() { echo "wiki-sync: $*" >&2; exit 1; }
[ -d "$SRC" ] || die "no $SRC"

# --- scrub -------------------------------------------------------------------
fail=0
if [ -f "$ROOT/secrets/carplay.env" ]; then
	while IFS= read -r v; do
		[ -n "$v" ] || continue
		if grep -rqF -- "$v" "$SRC"; then
			echo "wiki-sync: a car PSK or SSID digit group appears in wiki/ (${v%"${v#???}"}***)" >&2
			fail=1
		fi
	done <<EOF2
$(sed -n "s/^CAR_[AB]_PSK='\(.*\)'.*/\1/p; s/^CAR_[AB]_SSID='BMW\([0-9]*\) .*/\1/p" "$ROOT/secrets/carplay.env")
EOF2
fi
# Hostnames, private addressing and MAC-shaped strings. The repo's own docs use
# scrubbed placeholders; the wiki must too.
#
# The MAC pattern is ERE ({5}, not \{5\}). It was written in BRE inside
# grep -E first, which matches nothing -- so the check passed on a file
# containing a MAC. A safety check that cannot fail is worse than none, so
# each pattern below was verified against a deliberate violation.
for pat in 'alpina\.casa' '172\.16\.' '192\.168\.' '2603:' 'fd5c:' \
	'([0-9a-f]{2}:){5}[0-9a-f]{2}'; do
	if grep -rqE "$pat" "$SRC"; then
		echo "wiki-sync: wiki/ matches $pat -- scrub it before publishing" >&2
		grep -rlE "$pat" "$SRC" | sed 's/^/  /' >&2
		fail=1
	fi
done
[ "$fail" -eq 0 ] || die "refusing to publish"
echo "wiki-sync: scrub clean ($(ls -1 "$SRC"/*.md | wc -l | tr -d ' ') files)"
[ "${1:-}" = --check ] && exit 0

# --- publish -----------------------------------------------------------------
rm -rf "$TMP"
git clone -q "$WIKI" "$TMP" 2>/dev/null ||
	die "cannot clone the wiki. If it has never been used, create the first page
       in the web UI once -- GitHub will not create it from a push."
# README.md is this directory's own note, not a wiki page.
for f in "$SRC"/*.md; do
	[ "$(basename "$f")" = README.md ] && continue
	cp "$f" "$TMP/"
done
cd "$TMP"
if git diff --quiet && git diff --cached --quiet && [ -z "$(git status --porcelain)" ]; then
	echo "wiki-sync: already up to date"
	rm -rf "$TMP"
	exit 0
fi
git add -A
git -c user.name="${GIT_AUTHOR_NAME:-wiki-sync}" \
    -c user.email="${GIT_AUTHOR_EMAIL:-wiki-sync@localhost}" \
    commit -q -m "${WIKI_MESSAGE:-wiki: sync from wiki/ in the main repository}"
git push -q origin HEAD:master
echo "wiki-sync: published $(git ls-files | wc -l | tr -d ' ') pages"
rm -rf "$TMP"
