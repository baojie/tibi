# Security model

What tibi protects, against whom, and where the edges are. Configuration
details are in [DEPLOY.md](DEPLOY.md); the API rules in [API.md](API.md).

## Uploaded content

- User content is **never served as active content**. HTML, SVG, XHTML,
  JavaScript and XML come back as `text/plain` on raw URLs and as
  `application/octet-stream` downloads; every response carries
  `X-Content-Type-Options: nosniff`, and raw responses
  `Content-Security-Policy: default-src 'none'; sandbox`. Objects in S3
  are stored with a passive type too, in case a bucket is ever made public.
- View pages have a strict CSP (`default-src 'self'`, no inline scripts),
  are not framable, and send no referrer.
- Nothing is indexed: `X-Robots-Tag: noindex` everywhere, `robots.txt`
  disallows all.
- Image metadata (EXIF, including location) is stripped from JPEG, PNG and
  WebP, and from HEIC photos when they are converted. GIF and AVIF
  metadata, and HEIC stored without a converter, are kept as uploaded.

## Access

- API tokens are 32 random characters, stored as SHA-256 hashes, compared
  in constant time, never logged. Scopes: `upload`, `delete`,
  `read_private`, `admin`.
- **Private items** open on public URLs only with a `read_private` token
  or a signed share link. In the API they exist only for tokens with
  `read_private` and for the token that uploaded them. Give phones,
  browser extensions and scripts `upload` only.
- **Unlisted** items (the default) are public to anyone who has the link;
  IDs are 8 random characters (about 47 bits).
- Signed share links are HMAC-SHA256 over the item ID and expiry with a
  server key (`secret.key`, mode 0600).
- Burn-after-read items are deleted when revealed (a POST, so link
  previewers do not consume them; cross-site reveals are refused). The
  revealer gets a 10-minute link to the content.

## Abuse and cost

Per-token rate limit, upload size limits, per-token quotas (unfinished
uploads included), daily bandwidth caps per item and per instance, hotlink
protection, bounded image decoding and conversion. Public pages are not
rate limited by tibi; do that per IP in the reverse proxy.

## End-to-end encrypted pastes

`tibi paste --encrypt` (and `up`, `clip`, MCP `paste_text(encrypt=true)`)
encrypts on the client with AES-256-GCM. The key is in the link's fragment
(`#k=…`), which browsers never send to servers; the view page decrypts
with WebCrypto. An optional password is mixed in with PBKDF2-SHA256
(600 000 iterations). The format is in `internal/e2e` and the API document.

**What it protects against.** Anyone who gets the server's disk, database,
backups or object-storage bucket, a curious operator who only reads data,
and anyone who learns the item's ID or raw URL but not the full link: none
of them can read the content, its file name or its type.

**What it does not protect against.**

- **An operator who changes the server.** The decryption code is served
  by the same server; a malicious or compromised one could serve a
  version that sends the key elsewhere. This is inherent to decrypting in
  a web page (PrivateBin has the same limit). Decrypting with the CLI or
  your own client avoids it.
- **The link itself.** Whoever has the full link can read the content.
  It lives in browser history, in the clipboard (the CLI copies it), in
  chat logs — and, with MCP, in the AI agent's transcript. Add a password
  when the link travels somewhere you do not control.
- **Metadata.** The server sees the item's size (content plus a small
  header), when it was uploaded and by which token, its title and its
  `lang` hint — titles and `lang` are not encrypted; keep secrets out of
  them.

## Reporting a vulnerability

Please report security problems privately through the repository's
**Security → Report a vulnerability** page on GitHub (private
vulnerability reporting), not in a public issue.
