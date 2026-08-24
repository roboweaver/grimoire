package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/roboweaver/grimoire/internal/auth"
)

// authViaAppPasswordKey marks a request context as authenticated via a
// verified Application Password (as opposed to a session cookie or
// anonymous access). rest_comments.go consults it to decide whether the M4
// CSRF contract applies (Req 7.4/8.7: Application-Password-authenticated
// requests are exempt).
type appPasswordAuthMarker struct{}

var appPasswordAuthKey = appPasswordAuthMarker{}

// withAppPasswordAuth returns a copy of ctx carrying p as the authenticated
// principal, flagged as resolved via Application Password rather than a
// session cookie. It deliberately does not touch sessionKey: any session
// cookie also present on the request is ignored for the remainder of this
// request once Application Password auth has succeeded (Req 7.4 — the two
// mechanisms are mutually exclusive per request).
func withAppPasswordAuth(ctx context.Context, p auth.Principal) context.Context {
	ctx = context.WithValue(ctx, principalKey, p)
	ctx = context.WithValue(ctx, appPasswordAuthKey, true)
	return ctx
}

// isAppPasswordAuth reports whether the request context's principal (if any)
// was resolved via a verified Application Password.
func isAppPasswordAuth(ctx context.Context) bool {
	v, _ := ctx.Value(appPasswordAuthKey).(bool)
	return v
}

// ApplicationPasswordAuth resolves HTTP Basic "Authorization" credentials
// against s.appPasswords (Req 8.1/8.6). It is only mounted on the /wp-json
// route group (Req 8's Application Password auth is a REST-specific
// concern) and MUST run ahead of SessionMiddleware there:
//
//   - No "Authorization: Basic" header at all: not our concern, fall through
//     unchanged (session-cookie evaluation, or anonymous, proceeds normally
//     per Req 8.6's "absence is not invalid" carve-out).
//   - Header present: enforce the TLS/loopback transport gate first (Req
//     8.9), then verify. On success, set a request-scoped Principal flagged
//     as Application-Password-authenticated (CSRF-exempt, Req 8.7) and
//     continue. On ANY failure (transport gate or verification), respond
//     401 rest_invalid_credentials immediately and never call next — this
//     is intentionally unconditional, even if a valid session cookie is
//     also present on the request (Req 8.6, not to be softened).
func (s *Server) ApplicationPasswordAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login, secret, ok := r.BasicAuth()
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if !requestSatisfiesAppPasswordTransport(r, s.restRequireTLS, s.restTrustedProxyHeader) {
			writeRESTError(w, http.StatusUnauthorized, "rest_invalid_credentials", "Application Password authentication requires a secure (HTTPS) connection.")
			return
		}
		secret = strings.ReplaceAll(secret, " ", "")
		principal, err := s.appPasswords.Verify(r.Context(), login, secret, commentClientIP(r))
		if err != nil {
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				s.log.Error("application password verify failed", "err", err)
			}
			writeRESTError(w, http.StatusUnauthorized, "rest_invalid_credentials", "The Application Password credentials presented are invalid.")
			return
		}
		next.ServeHTTP(w, r.WithContext(withAppPasswordAuth(r.Context(), principal)))
	})
}

// requestSatisfiesAppPasswordTransport reports whether r's connection meets
// the transport-security bar Application Password auth requires (Req 8.9):
// TLS-terminated directly (r.TLS != nil), TLS-terminated by a trusted
// reverse proxy (trustedProxyHeader set and reporting "https", an
// operator-declared trust boundary matching AuthConfig.Secure's own
// posture), or connected from a loopback peer (r.RemoteAddr — the actual TCP
// connection, never the client-suppliable r.Host). When requireTLS is false
// the gate is disabled entirely (operator override).
func requestSatisfiesAppPasswordTransport(r *http.Request, requireTLS bool, trustedProxyHeader string) bool {
	if !requireTLS {
		return true
	}
	if r.TLS != nil {
		return true
	}
	if trustedProxyHeader != "" && strings.EqualFold(r.Header.Get(trustedProxyHeader), "https") {
		return true
	}
	return isLoopbackPeer(r.RemoteAddr)
}

// isLoopbackPeer reports whether remoteAddr (an http.Request.RemoteAddr,
// "host:port" as set by the net/http server from the actual TCP peer) is a
// loopback address, matching WordPress's own allowance for Application
// Passwords on a local development install without TLS.
//
// This MUST be based on the actual connection peer (r.RemoteAddr), never on
// r.Host: Host is taken verbatim from the client-supplied Host header (or
// request line) on a plain HTTP request and is entirely attacker-controlled,
// so a remote attacker could otherwise satisfy the loopback exception simply
// by sending "Host: localhost" over plaintext HTTP and bypass the
// TLS-required gate entirely (Req 8.9 is a transport-security control, not a
// header-matching one).
func isLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port present (e.g. a bare IP, or a test harness value that
		// isn't "host:port"): fall back to treating the whole value as the
		// host, matching net/http server behavior where RemoteAddr is
		// always "IP:port" in practice.
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	// Not a parseable IP literal (e.g. "localhost" in a hand-built test
	// request): match by name too, since real net/http servers always
	// populate RemoteAddr with a literal IP and this only matters for tests.
	return host == "localhost"
}
