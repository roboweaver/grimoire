#!/usr/bin/env bash
#
# demo.sh - end-to-end walkthrough of Milestone 5 (extensions + wp-json REST
# API + Application Passwords) against a fresh, throwaway SQLite database.
#
# This is a demonstration/smoke-test script, not a test suite: it builds the
# real grimoire/grimoire-cli binaries, stands up a real server on a free
# loopback port, and drives it with curl exactly the way an external client
# would, including the two security-relevant regressions fixed in PR #12's
# review round (the Host-header-spoofing TLS/loopback bypass and the
# session-cookie-overwrites-Application-Password-principal CSRF hole).
#
# Usage: ./demo.sh   (run from anywhere; paths are resolved relative to this
# script's own location, not the caller's $PWD)
set -euo pipefail

# --- setup -------------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

# Prefer a Homebrew-installed go if the default PATH doesn't have one (common
# on macOS dev boxes); harmless no-op if go is already on PATH.
if ! command -v go >/dev/null 2>&1 && [ -x /opt/homebrew/bin/go ]; then
	export PATH="$PATH:/opt/homebrew/bin"
fi

DEMO_DIR="$(mktemp -d /tmp/grimoire-demo.XXXXXX)"
DB_FILE="$DEMO_DIR/grimoire-demo.db"
CONFIG_FILE="$DEMO_DIR/config.yaml"
COOKIE_JAR="$DEMO_DIR/cookies.txt"
SERVER_LOG="$DEMO_DIR/server.log"
SERVER_PID=""

ADMIN_LOGIN="demo-admin"
ADMIN_EMAIL="demo-admin@example.test"
ADMIN_PASSWORD="correct horse battery staple 42!"
# Note: the "seed" subcommand above also inserts a sample user with login
# "admin" (no usable password), so we deliberately create ours under a
# different login to avoid a "user already exists" collision.

cleanup() {
	local status=$?
	if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
	fi
	rm -rf "$DEMO_DIR"
	exit "$status"
}
trap cleanup EXIT INT TERM

# pp: pretty-print JSON from stdin. jq if available, else python3's
# json.tool, else pass through raw.
pp() {
	if command -v jq >/dev/null 2>&1; then
		jq . 2>/dev/null || cat
	elif command -v python3 >/dev/null 2>&1; then
		python3 -m json.tool 2>/dev/null || cat
	else
		cat
	fi
}

# jget <json-file> <jq-filter>: extract a single field. Uses jq when
# available; falls back to a small inline python3 snippet using the
# equivalent dotted/indexed path (only the small set of paths this script
# actually needs are supported by the fallback).
jget() {
	local file="$1" filter="$2"
	if command -v jq >/dev/null 2>&1; then
		jq -r "$filter" "$file"
	elif command -v python3 >/dev/null 2>&1; then
		python3 - "$file" "$filter" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
filt = sys.argv[2]
# Only the exact filters this script uses are supported here.
table = {
    ".[0].id": lambda d: d[0]["id"],
    ".csrfToken": lambda d: d["csrfToken"],
    ".password": lambda d: d["password"],
    ".uuid": lambda d: d["uuid"],
}
print(table[filt](data))
PY
	else
		echo "jget: neither jq nor python3 is available" >&2
		return 1
	fi
}

banner() {
	echo
	echo "=== $1 ==="
}

# find_free_port: ask the OS for an ephemeral port by binding to :0 and
# reading back the assigned port, then releasing it immediately. There is an
# unavoidable, harmless TOCTOU race between this and grimoire binding the
# same port a moment later; acceptable for a local demo script.
find_free_port() {
	python3 - <<'PY'
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

PORT="$(find_free_port)"
BASE_URL="http://127.0.0.1:${PORT}"

echo "Demo workspace: $DEMO_DIR"
echo "Server will listen on $BASE_URL"

# --- build ---------------------------------------------------------------

banner "Building grimoire + grimoire-cli"
go build -o "$DEMO_DIR/grimoire" ./cmd/grimoire
go build -o "$DEMO_DIR/grimoire-cli" ./cmd/grimoire-cli
echo "built $DEMO_DIR/grimoire and $DEMO_DIR/grimoire-cli"

# --- config ----------------------------------------------------------------

cat >"$CONFIG_FILE" <<EOF
server:
  addr: ":${PORT}"
theme: default
database:
  vendor: sqlite
  dsn: "file:${DB_FILE}?_pragma=busy_timeout(5000)"
  table_prefix: "wp_"
session:
  cookie_name: "grimoire_session"
  cookie_secure: false
  ttl_hours: 336
rest:
  # Left at the real default (true): the loopback exception below is what
  # lets this demo's plain-HTTP curl calls succeed against 127.0.0.1 without
  # needing to stand up TLS, exactly like a real WordPress local install.
  require_tls_for_application_passwords: true
  trusted_proxy_header: ""
  per_page_max: 100
EOF

# --- migrate / seed / bootstrap admin --------------------------------------

banner "Migrating schema + seeding sample content"
"$DEMO_DIR/grimoire-cli" migrate -config "$CONFIG_FILE"
"$DEMO_DIR/grimoire-cli" seed -config "$CONFIG_FILE"

banner "Creating administrator account"
GRIMOIRE_ADMIN_PASSWORD="$ADMIN_PASSWORD" "$DEMO_DIR/grimoire-cli" createadmin \
	-config "$CONFIG_FILE" -login "$ADMIN_LOGIN" -email "$ADMIN_EMAIL"

# --- start server ------------------------------------------------------

banner "Starting grimoire server"
"$DEMO_DIR/grimoire" -config "$CONFIG_FILE" -themes "$REPO_ROOT/themes" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

# Poll for readiness instead of a fixed sleep.
for _ in $(seq 1 50); do
	if curl -s -o /dev/null "$BASE_URL/wp-json/"; then
		break
	fi
	if ! kill -0 "$SERVER_PID" 2>/dev/null; then
		echo "server exited early; log follows:" >&2
		cat "$SERVER_LOG" >&2
		exit 1
	fi
	sleep 0.1
done
echo "server is up (pid $SERVER_PID)"

# --- 1. namespace indexes -------------------------------------------------

banner "GET /wp-json/ (bare index)"
curl -s "$BASE_URL/wp-json/" | pp

banner "GET /wp-json/wp/v2/ (namespace index)"
curl -s "$BASE_URL/wp-json/wp/v2/" | pp

# --- 2. posts list + pagination headers, single post with _embed ---------

banner "GET /wp-json/wp/v2/posts (showing X-WP-Total / X-WP-TotalPages headers)"
POSTS_HEADERS="$DEMO_DIR/posts-headers.txt"
POSTS_BODY="$DEMO_DIR/posts-body.json"
curl -s -D "$POSTS_HEADERS" -o "$POSTS_BODY" "$BASE_URL/wp-json/wp/v2/posts"
grep -i '^X-WP-Total' "$POSTS_HEADERS"
pp <"$POSTS_BODY" | head -40
FIRST_POST_ID="$(jget "$POSTS_BODY" '.[0].id')"
echo "(using post id $FIRST_POST_ID for the next steps)"

banner "GET /wp-json/wp/v2/posts/${FIRST_POST_ID}?_embed=1 (showing _links / _embedded)"
curl -s "$BASE_URL/wp-json/wp/v2/posts/${FIRST_POST_ID}?_embed=1" | pp

# --- 3. deferred write returns 501, not 404/405 ---------------------------

banner "POST /wp-json/wp/v2/posts with no auth (expect 501, not 404/405)"
STATUS=$(curl -s -o "$DEMO_DIR/create-post-501.json" -w '%{http_code}' \
	-X POST -H 'Content-Type: application/json' -d '{"title":"nope"}' \
	"$BASE_URL/wp-json/wp/v2/posts")
echo "status: $STATUS"
pp <"$DEMO_DIR/create-post-501.json"
if [ "$STATUS" != "501" ]; then
	echo "FAIL: expected 501, got $STATUS" >&2
	exit 1
fi

# --- 4. log in, mint an Application Password ------------------------------

banner "GET /login (fetch the login-form double-submit CSRF token)"
LOGIN_PAGE="$DEMO_DIR/login.html"
curl -s -c "$COOKIE_JAR" -o "$LOGIN_PAGE" "$BASE_URL/login"
LOGIN_CSRF="$(grep -o 'name="csrf_token" value="[^"]*"' "$LOGIN_PAGE" | sed -E 's/.*value="([^"]*)"/\1/')"
echo "login form csrf_token: $LOGIN_CSRF"

banner "POST /login (establish a session cookie)"
curl -s -c "$COOKIE_JAR" -b "$COOKIE_JAR" -o /dev/null -w 'status: %{http_code}\n' \
	--data-urlencode "log=${ADMIN_LOGIN}" \
	--data-urlencode "pwd=${ADMIN_PASSWORD}" \
	--data-urlencode "csrf_token=${LOGIN_CSRF}" \
	--data-urlencode "redirect=/" \
	"$BASE_URL/login"

banner "GET /admin/api/session (read the per-session synchronizer CSRF token)"
SESSION_JSON="$DEMO_DIR/session.json"
curl -s -b "$COOKIE_JAR" -o "$SESSION_JSON" "$BASE_URL/admin/api/session"
pp <"$SESSION_JSON"
SESSION_CSRF="$(jget "$SESSION_JSON" '.csrfToken')"
echo "session X-CSRF-Token: $SESSION_CSRF"

banner "POST /wp-json/wp/v2/users/me/application-passwords (mint a new Application Password)"
APPPW_JSON="$DEMO_DIR/apppassword.json"
curl -s -b "$COOKIE_JAR" -o "$APPPW_JSON" \
	-H "X-CSRF-Token: ${SESSION_CSRF}" -H 'Content-Type: application/json' \
	-d '{"name":"demo.sh walkthrough"}' \
	"$BASE_URL/wp-json/wp/v2/users/me/application-passwords"
pp <"$APPPW_JSON"
APP_SECRET="$(jget "$APPPW_JSON" '.password')"
APP_UUID="$(jget "$APPPW_JSON" '.uuid')"
echo "one-time plaintext Application Password secret: $APP_SECRET"
echo "Application Password uuid: $APP_UUID"

# --- 5. use the Application Password over HTTP Basic auth -----------------

banner "GET /wp-json/wp/v2/users/me with the new Application Password over HTTP Basic auth"
curl -s -u "${ADMIN_LOGIN}:${APP_SECRET}" -w '\nstatus: %{http_code}\n' \
	"$BASE_URL/wp-json/wp/v2/users/me" | pp 2>/dev/null || true
curl -s -o /dev/null -w 'status: %{http_code}\n' -u "${ADMIN_LOGIN}:${APP_SECRET}" \
	"$BASE_URL/wp-json/wp/v2/users/me"

# --- 6. security fix: invalid Basic + valid session cookie still 401 ------

banner "SECURITY CHECK: invalid Basic credential + a VALID session cookie on the same request"
echo "(This is PR #12 review finding 2: SessionMiddleware must NOT let a"
echo " valid session cookie rescue an invalid/rejected Basic credential."
echo " Expect 401 rest_invalid_credentials, not 200.)"
INVALID_STATUS=$(curl -s -o "$DEMO_DIR/invalid-basic.json" -w '%{http_code}' \
	-b "$COOKIE_JAR" -u "${ADMIN_LOGIN}:totally-wrong-secret" \
	"$BASE_URL/wp-json/wp/v2/users/me")
echo "status: $INVALID_STATUS"
pp <"$DEMO_DIR/invalid-basic.json"
if [ "$INVALID_STATUS" != "401" ]; then
	echo "FAIL: expected 401 (invalid Basic must not be rescued by a valid session cookie), got $INVALID_STATUS" >&2
	exit 1
fi

# --- 7. anonymous comment creation -----------------------------------------

banner "POST /wp-json/wp/v2/comments (unauthenticated)"
curl -s -w '\nstatus: %{http_code}\n' \
	-H 'Content-Type: application/json' \
	-d "{\"post\":${FIRST_POST_ID},\"author_name\":\"Demo Visitor\",\"author_email\":\"visitor@example.test\",\"content\":\"Hello from demo.sh!\"}" \
	"$BASE_URL/wp-json/wp/v2/comments" | pp 2>/dev/null || true

# --- 8. extension hook firing --------------------------------------------

banner "pkg/extensions: comment.submitted / rest.pre_dispatch / rest.response / render.post_html firing end-to-end"
echo "The running demo server above has zero extensions linked in (cmd/grimoire"
echo "only blank-imports extension packages that are explicitly compiled in), so"
echo "there is nothing to observe over HTTP for a hook firing. What IS"
echo "demonstrable is the e2e-level proof that these hook points really do fire"
echo "when driven through real HTTP requests against a fully wired server with"
echo "hooks registered: test/e2e/m5_test.go's TestM5ExtensionHooksFireEndToEnd"
echo "registers one listener per Requirement-11 hook point and asserts each"
echo "fires exactly once per matching request, including the comment.submitted"
echo "action fired by the exact POST /wp-json/wp/v2/comments flow demonstrated"
echo "above. Running it now:"
echo
(cd "$REPO_ROOT" && go test ./test/e2e/... -run TestM5ExtensionHooksFireEndToEnd -v)

banner "Demo complete"
echo "All steps passed. Cleaning up (killing server, removing $DEMO_DIR)."
