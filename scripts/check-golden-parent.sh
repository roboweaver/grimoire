#!/bin/sh
# M19 task 6.1/6.2: prove the built-in golden regeneration only added
# `"parent": 0` to REST page objects.
#
# For each golden file that differs from the base ref (default: main):
#   1. no line is removed;
#   2. every added line is `"parent": 0` (optional trailing comma);
#   3. the number of added lines equals the number of REST page objects
#      (type "page", in an entry whose name starts with "rest ") that lack a
#      `parent` key in the base version of the file. Admin entries are not
#      counted: the admin page detail still omits `parent` when 0.
#
# Usage: scripts/check-golden-parent.sh [base-ref]
set -eu

BASE="${1:-main}"
DIR="cmd/grimoire/testdata/builtin_golden"
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }

status=0
checked=0
for f in "$DIR"/*.json; do
	if git diff --quiet "$BASE" -- "$f"; then
		continue
	fi
	checked=$((checked + 1))
	diff_out="$(git diff -U0 "$BASE" -- "$f")"

	removed="$(printf '%s\n' "$diff_out" | grep -E '^-[^-]' || true)"
	if [ -n "$removed" ]; then
		echo "FAIL $f: lines removed:" >&2
		printf '%s\n' "$removed" >&2
		status=1
		continue
	fi

	added="$(printf '%s\n' "$diff_out" | grep -E '^\+[^+]' || true)"
	bad="$(printf '%s\n' "$added" | grep -Ev '^\+[[:space:]]+"parent": 0,?$' | grep -v '^$' || true)"
	if [ -n "$bad" ]; then
		echo "FAIL $f: added lines other than \"parent\": 0:" >&2
		printf '%s\n' "$bad" >&2
		status=1
		continue
	fi

	actual=0
	if [ -n "$added" ]; then
		actual="$(printf '%s\n' "$added" | wc -l | tr -d ' ')"
	fi
	expected="$(git show "$BASE:$f" | jq '[.[] | select(.name | startswith("rest ")) | .. | objects | select(.type == "page" and (has("parent") | not))] | length')"
	if [ "$actual" != "$expected" ]; then
		echo "FAIL $f: added $actual parent lines, expected $expected (REST page objects without parent in $BASE)" >&2
		status=1
		continue
	fi
	echo "ok   $f: $actual added, $expected expected"
done

if [ "$checked" -eq 0 ]; then
	echo "no changes to check"
fi
exit "$status"
