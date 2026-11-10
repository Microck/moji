# Extensionless Google Fonts regression

Run `node tests/fonts/extensionless.mjs` after `npm ci`.

This exercises the real Turbopack production compiler against a loopback font
server and a test-only Google Fonts CSS response containing an extensionless
URL with ampersands, equals signs and percent escapes. It asserts the full query
reaches the server and the exact existing FontTools WOFF2 fixture is emitted.
The build uses `.next-font-regression`, leaving the normal `.next` output alone.
Normal production and browser builds still fetch the real Google Sans Flex font.

Next 16.2.10 fails on this response with `next/font/google queries have exactly
one entry`. The fix landed in Next 16.4.3; the docs pin 16.4.6 to include subsequent
security fixes too. This test does not test availability of Google's font service.

Upstream diagnosis: https://github.com/vercel/next.js/issues/99114
Upstream fix: https://github.com/vercel/next.js/pull/100158
Stable backport: https://github.com/vercel/next.js/releases/tag/v16.4.3
Fixture provenance: `internal/fontconvert/testdata/README.md`.
