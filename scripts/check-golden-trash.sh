#!/bin/sh
# M22 task 6.3: prove the built-in golden regeneration changed only what
# design.md "Golden changes" allows.
#
# Usage: scripts/check-golden-trash.sh <base-ref> <pr1|pr2>
#
# For each of with_registry.json and without_registry.json (working tree vs
# the base ref):
#   1. the first 49 entry names equal the base names in order;
#   2. pr2: the first 49 entries equal the base entries; pr1: the same for
#      every index except 7, 8 and 13;
#   3. pr1, index 7 and 13: status 200, no "deleted" key, status "trash",
#      slug is the base previous slug plus "__trashed", and the body minus
#      status/slug/link/modified/modified_gmt equals base previous minus the
#      same keys;
#   4. pr1, index 8: status 200 and body equal to the new index 7 body;
#   5. appended entries have the expected names and statuses;
#   6. both files agree on the appended entries.
# Requires jq.
set -eu
BASE="${1:-}"
MODE="${2:-}"
USAGE="usage: $0 <base-ref> <pr1|pr2>"
[ -n "$BASE" ] || { echo "$USAGE" >&2; exit 2; }
if [ "$MODE" != pr1 ] && [ "$MODE" != pr2 ]; then
	echo "$USAGE" >&2
	exit 2
fi
DIR="cmd/grimoire/testdata/builtin_golden"
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }

if [ "$MODE" = pr1 ]; then
	EXP_NAMES='["rest trash create","rest trash delete","rest trash delete again","rest trash list default","rest trash list status trash","rest trash list anonymous","rest trash restore","rest trash force delete","rest trash gone"]'
	EXP_STATUS='[201,200,410,200,200,403,200,200,404]'
	CHANGED=" 7 8 13 "
else
	EXP_NAMES='["admin trash create","admin trash delete","admin trash get","admin trash list","admin trash restore","admin trash delete again","admin trash force","admin trash gone","admin empty trash"]'
	EXP_STATUS='[201,204,200,200,200,204,204,404,200]'
	CHANGED=" "
fi
STRIP='del(.status, .slug, .link, .modified, .modified_gmt)'
status=0
fail() {
	echo "FAIL $1: $2" >&2
	status=1
}

for name in without_registry with_registry; do
	f="$DIR/$name.json"
	tmp="$(mktemp)"
	if ! git show "$BASE:$f" >"$tmp" 2>/dev/null; then
		fail "$f" "cannot read from $BASE"
		rm -f "$tmp"
		continue
	fi
	nb="$(jq 'length' "$tmp")"
	if [ "$nb" -lt 49 ]; then
		fail "$f" "base has $nb entries, want at least 49"
		rm -f "$tmp"
		continue
	fi
	ok=1
	# 1. names of the first 49 entries.
	if [ "$(jq -c '[.[0:49][].name]' "$tmp")" != "$(jq -c '[.[0:49][].name]' "$f")" ]; then
		fail "$f" "first 49 entry names differ from $BASE"
		ok=0
	fi
	# 2. unchanged entries.
	i=0
	while [ "$i" -lt 49 ]; do
		case "$CHANGED" in
		*" $i "*) ;;
		*)
			a="$(jq -S -c ".[$i]" "$tmp")"
			b="$(jq -S -c ".[$i]" "$f")"
			if [ "$a" != "$b" ]; then
				fail "$f" "entry $i ($(jq -r ".[$i].name" "$tmp")) changed but is not allowed to"
				ok=0
			fi
			;;
		esac
		i=$((i + 1))
	done
	if [ "$MODE" = pr1 ]; then
		# 3. entries 7 and 13.
		for i in 7 13; do
			if ! jq -e --argjson i "$i" --slurpfile b "$tmp" '
				.[$i] as $n | $b[0][$i].body.previous as $p |
				$n.status == 200
				and ($n.body | has("deleted") | not)
				and $n.body.status == "trash"
				and $n.body.slug == ($p.slug + "__trashed")
				and (($n.body | '"$STRIP"') == ($p | '"$STRIP"'))' "$f" >/dev/null; then
				fail "$f" "entry $i is not the expected trashed object"
				ok=0
			fi
		done
		# 4. entry 8.
		if ! jq -e '.[8].status == 200 and (.[8].body == .[7].body)' "$f" >/dev/null; then
			fail "$f" "entry 8 is not 200 with the body of entry 7"
			ok=0
		fi
	fi
	# 5. appended entries.
	if [ "$(jq -c '[.[49:][].name]' "$f")" != "$EXP_NAMES" ]; then
		fail "$f" "appended entry names are $(jq -c '[.[49:][].name]' "$f"), want $EXP_NAMES"
		ok=0
	fi
	if [ "$(jq -c '[.[49:][].status]' "$f")" != "$EXP_STATUS" ]; then
		fail "$f" "appended statuses are $(jq -c '[.[49:][].status]' "$f"), want $EXP_STATUS"
		ok=0
	fi
	rm -f "$tmp"
	if [ "$ok" -eq 1 ]; then
		echo "ok   $f"
	fi
done
# 6. both files agree on the appended entries.
if [ "$(jq -S -c '.[49:]' "$DIR/without_registry.json")" != "$(jq -S -c '.[49:]' "$DIR/with_registry.json")" ]; then
	fail "$DIR" "without_registry and with_registry differ on the appended entries"
fi
exit "$status"
