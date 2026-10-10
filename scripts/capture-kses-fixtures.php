<?php
/**
 * capture-kses-fixtures.php — WordPress kses parity oracle capture.
 *
 * COMMITTED, RE-RUNNABLE, AND DELIBERATELY NOT RUN BY THE TEST SUITE.
 *
 * This script is the provenance of internal/sanitize/testdata/parity/fixtures.json.
 * It runs ONCE, BY HAND, inside a running WordPress container — it is never
 * invoked by `go test`, CI, or any Go code (Requirement 9.12: committed tests
 * stay database-free and network-free). The Go parity test asserts against the
 * captured JSON only; it never shells out to PHP.
 *
 * Usage (against the podman WordPress stack: wp-wordpress, wp-mysql, prefix
 * `accuweaver`), from the directory holding WordPress core:
 *
 *   podman exec -i wp-wordpress php /path/to/capture-kses-fixtures.php \
 *       < internal/sanitize/testdata/parity/inputs.txt \
 *       > internal/sanitize/testdata/parity/fixtures.json
 *
 * It reads the input list from testdata/parity/inputs.txt — the same inputs the
 * committed fixtures are keyed on — one record per line, tab-separated:
 *
 *   <id>\t<kind>\t<tier>\t<input>
 *
 * where <kind> is one of post_content|post_excerpt|post_title|comment_content,
 * <tier> is one of A|B|C, and <input> is the raw input with \n, \t and \\
 * escapes (so a single line can carry newlines). It emits fixtures.json with,
 * per record: id, kind, tier, input, wordpress_of_input, grimoire,
 * wordpress_of_grimoire, divergence.
 *
 * It calls the kses entry points DIRECTLY, never the *_save_pre filters:
 *
 *   | Tier | Oracle call                        |
 *   |------|------------------------------------|
 *   | A    | wp_kses($in, $allowedtags)         |
 *   | B    | wp_kses_post($in)                  |
 *   | C    | identity — no call                 |
 *
 * NOT wp_filter_kses / wp_filter_post_kses: those wrap addslashes(stripslashes())
 * for WordPress's $_POST handling and leak spurious backslashes into the output
 * (wp_filter_kses returns <a href=\"http://x/\"> where wp_kses returns
 * <a href="http://x/">). Capturing through the filter would bake a WordPress
 * request-handling artifact into grimoire's parity oracle.
 *
 * The `grimoire` and `wordpress_of_grimoire` columns are filled by the operator
 * after a second pass: `grimoire` is grimoire's own SanitizeAt output for the
 * input (the source of truth for grimoire), and `wordpress_of_grimoire` is this
 * script's oracle applied to that grimoire output — which is P8:
 * kses(sanitize(k, t, x)) == sanitize(k, t, x). The committed fixtures already
 * carry both, computed against the current implementation; re-running this
 * script re-derives the WordPress columns for a diff.
 */

if (!function_exists('wp_kses')) {
    // Load just the kses machinery and its dependencies. Inside the container
    // WordPress core is already on the include path; wp-load.php brings in
    // $allowedtags, $allowedposttags and wp_allowed_protocols().
    require_once rtrim(getenv('WP_PATH') ?: '/var/www/html', '/') . '/wp-load.php';
}

global $allowedtags, $allowedposttags;

/** Decode the \n \t \\ escapes used in inputs.txt so one line carries newlines. */
function decode_input(string $s): string
{
    return strtr($s, ['\\n' => "\n", '\\t' => "\t", '\\\\' => '\\']);
}

/** Apply the tier's oracle to a raw string. Tier C is identity (no kses call). */
function oracle(string $tier, string $in)
{
    global $allowedtags;
    switch ($tier) {
        case 'A':
            return wp_kses($in, $allowedtags);
        case 'B':
            return wp_kses_post($in);
        case 'C':
            return $in; // identity — the unfiltered bypass
        default:
            fwrite(STDERR, "unknown tier: {$tier}\n");
            exit(1);
    }
}

$fixtures = [];
$fh = fopen('php://stdin', 'r');
while (($line = fgets($fh)) !== false) {
    $line = rtrim($line, "\r\n");
    if ($line === '' || $line[0] === '#') {
        continue; // blank line or comment
    }
    $parts = explode("\t", $line, 4);
    if (count($parts) < 4) {
        fwrite(STDERR, "malformed line: {$line}\n");
        continue;
    }
    [$id, $kind, $tier, $rawInput] = $parts;
    $input = decode_input($rawInput);

    // wordpress_of_input is the oracle on the raw input: the input-output oracle
    // the divergence enumeration is derived from (not asserted by the Go test).
    $wpOfInput = oracle($tier, $input);

    // grimoire is NOT computed here (PHP cannot run Go); the operator fills it
    // from grimoire's SanitizeAt output. wordpress_of_grimoire is the oracle on
    // that grimoire value — P8. Emitted here as a placeholder the operator
    // reconciles against the committed Go-derived values.
    $fixtures[] = [
        'id'                    => $id,
        'kind'                  => $kind,
        'tier'                  => $tier,
        'input'                 => $input,
        'wordpress_of_input'    => $wpOfInput,
        'grimoire'              => null, // filled from grimoire SanitizeAt
        'wordpress_of_grimoire' => null, // = oracle($tier, grimoire); this is P8
        'divergence'            => null, // "none" unless wordpress_of_input != grimoire
    ];
}
fclose($fh);

echo json_encode($fixtures, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), "\n";
