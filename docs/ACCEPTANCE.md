# Acceptance record

Every "done when" item of `PLAN.md` versions 0.0.1–0.0.17, and where it is
checked. **CI** means it runs on every push (`.github/workflows/ci.yml`:
jobs `test`, `test-s3`, `android`, `package`); Go tests run in `test` on
local disk and again in `test-s3` against an S3-compatible store. **Manual**
entries were done once, on the date given, and are not repeated
automatically.

## 0.0.1 — skeleton

| criterion | check |
|---|---|
| `curl --data-binary` and `tibi up` return a URL whose `/dl` has identical bytes | CI: `TestUploadDownloadRoundTrip`, `TestLoginAndUpload` |
| 401 without / with a revoked token | CI: `TestAuth`, `TestAuthErrorsEveryOperation` |
| 413 over the limit | CI: `TestTooLarge` |
| `/robots.txt` disallows all; `X-Robots-Tag` on every public route | CI: `TestRobotsAndNoindex` |
| `evil.html` never served as `text/html` | CI: `TestActiveContentNeverServedAsHTML` |

## 0.0.2 — image hosting

| criterion | check |
|---|---|
| PNG round-trips via `.png` raw URL as `image/png` | CI: `TestPNGRoundTrip` |
| JPEG GPS EXIF stripped; `keep_meta=1` keeps it | CI: `TestJPEGMetadataStripped`, `TestStripJPEG` |
| same file twice → two IDs, one blob | CI: `TestSameContentTwiceSharesBlob` |
| SVG is kind `file`, never `image/svg+xml` | CI: `TestSVGIsFile` |
| `tibi clip` uploads a copied screenshot | CI: `TestClip` (fake clipboard); manual by the owner, 2026-11-20 |

## 0.0.3 — pastebin

| criterion | check |
|---|---|
| `tibi paste -l python` prints one URL, highlighted | CI: `TestPaste`, `TestPasteHighlighted` |
| `cat main.go \| tibi` infers Go | CI: `TestLangInferenceAndErrors`, `TestInferLang` |
| GBK file shows correct Chinese, `charset=GBK` | CI: `TestGBKTranscoded` |
| 5 MiB log: truncated view, working raw link | CI: `TestLargeTextTruncated` |

## 0.0.4 — management

| criterion | check |
|---|---|
| expired item 404s; blob gone after GC when unreferenced | CI: `TestExpiryAndGC`, `TestShortExpiryNeverEarly` |
| deleting one of two identical uploads keeps the blob | CI: `TestDeleteKeepsSharedBlob` |
| private: 404 without token, 200 with `read_private` | CI: `TestPrivate` |
| slugs redirect; reserved slugs rejected | CI: `TestSlugs` |
| burn item survives raw prefetch and link-preview fetch, 404s after one reveal | CI: `TestBurnAfterRead`, `TestBurnConcurrentRevealAndKinds` |

## 0.0.5 — S3 / Backblaze B2

| criterion | check |
|---|---|
| whole suite passes on `fs` and `s3` | CI: jobs `test` and `test-s3` (versitygw) |
| `redirect` mode: images 302 to a working presigned URL, text proxied as `text/plain` | CI: `TestRedirectDelivery`, `TestRedirectFallsBackOnFS` |
| `fs` → `s3` migration keeps every raw URL byte-identical | CI: `TestMigrateFSToS3` |
| `Range: bytes=0-99` → 206 with 100 bytes | CI: `TestRange` |
| real B2 bucket | manual, 2026-11-20: full suite against a real B2 bucket; redirect, Range and deletion of all object versions checked with the real binary (steps in `DEPLOY.md`) |

## 0.0.6 — media

| criterion | check |
|---|---|
| mp4 plays in the view page and seeks (Range) | CI: `TestVideoAndAudio`, `TestRange`; manual, 2026-11-20: H.264 mp4 in headless Chrome shows frame and duration, fetched with 206 |
| HEIC displays as JPEG with `orig_mime=image/heic` | CI: `TestHEICConverted`, `TestHEICRealConverter` (must pass, not skip, in CI) |
| converter `off`: HEIC stored as-is, offered for download | CI: `TestHEICWithoutConverter` |
| thumbnail for each image, GC'd with it | CI: `TestThumbnails`, `TestOrphans` |

## 0.0.7 — search and contract

| criterion | check |
|---|---|
| English word, code identifier, 2-character Chinese substring each found | CI: `TestSearch` |
| deleted/expired items never appear | CI: `TestExpiredAndDeletedHidden`, `TestSearch` |
| contract test fails when a handler diverges from the spec | CI: `TestValidatorCatchesDivergence`; every API response in the suite is validated, and `TestMain` fails on undocumented or never-exercised responses |

## 0.0.8 — collections and forks

| criterion | check |
|---|---|
| `tibi up --album a.png b.png c.txt` → one collection URL, in order | CI: `TestAlbumCommand`, `TestAlbumUpload` |
| deleting the collection soft-deletes its items | CI: `TestAlbumUpload` |
| forking: new ID with `forked_from`, original unchanged | CI: `TestFork`, `TestForkCommand` |

## 0.0.9 — web, previews, share links, i18n

| criterion | check |
|---|---|
| pasting an image into the upload page yields a link | CI: `scripts/e2e/paste-upload.mjs` (Chrome) |
| unfurl gets OG tags for unlisted images, not private/burn/encrypted | CI: `TestOpenGraph`, `TestEncryptedNoPlaintext` |
| signed link works until expiry, then 404s | CI: `TestShareLink`, `TestSign` |
| Chinese UI with `Accept-Language: zh-CN` | CI: `TestChineseUI`, `paste-upload.mjs` |

## 0.0.10 — cost control and backups

| criterion | check |
|---|---|
| over the daily cap: raw 429, view page explains | CI: `TestDailyCaps` |
| hotlinking blocked, empty referer allowed | CI: `TestHotlinkProtection` |
| over quota → 413 with a clear message | CI: `TestQuota` |
| export A → import into empty B reproduces every URL byte-identically (fs and s3) | CI: `TestExportImportFS`, `TestExportImportToS3`, `TestExportImportS3` |

## 0.0.11 — packaging and integrations

| criterion | check |
|---|---|
| `docker run` with a data volume serves uploads and converts HEIC | CI: image builds (`package`); manual, 2026-11-20: uploads, HEIC conversion, data kept across restart, non-root |
| `.deb` installs a working systemd unit | CI: package builds (`package`); manual, 2026-11-20: installed in a systemd container — enable, health, upload, restart, HEIC under the unit's sandbox |
| PicGo plugin uploads | manual, 2026-11-20: real PicGo core 1.8.1 |
| ShareX config uploads | **partly**: the `.sxcu` request was replayed with curl; not yet run in ShareX on Windows |
| tagging a version publishes all artifacts | **deliberately not exercised**: the release workflow is gated on `RELEASE_ENABLED` until the project goes public; GoReleaser snapshots build in CI |

## 0.0.12 — MCP

| criterion | check |
|---|---|
| a client calls `paste_text` and `upload_file`, gets working URLs | CI: `TestPasteAndUpload`, `TestProtocol`; manual, 2026-11-20: official MCP TypeScript SDK 1.32.2 client against `tibi mcp` |
| bad token / too large → tool errors, not crashes | CI: `TestToolErrors` |

## 0.0.13 — resumable uploads

| criterion | check |
|---|---|
| 500 MiB upload killed at ~50% resumes without re-sending, correct item | CI: `TestResumableInterruptAndResume`, `TestUpUsesResumableAndResumes`; manual, 2026-11-20: 500 MiB, CLI killed at 251 MiB, resumed, 248 MiB more received, SHA-256 equal |
| abandoned partial uploads cleaned after 24 h | CI: `TestResumableCleanup` |

## 0.0.14 — end-to-end encryption

| criterion | check |
|---|---|
| server DB and blobs contain no plaintext (marker grep, with a control) | CI: `TestEncryptedNoPlaintext` |
| `#k=` link decrypts in the browser | CI: `scripts/e2e/decrypt.mjs` (Chrome): text, password, image |
| wrong / missing key: clear error | CI: `decrypt.mjs` |
| excluded from search and OpenGraph | CI: `TestEncryptedNoPlaintext` |

## 0.0.15 — Chrome extension

| criterion | check |
|---|---|
| image → URL in clipboard; selection → paste; screenshot → image; popup lists recent | CI: `scripts/e2e/extension.mjs` (unpacked extension in Chrome; clipboard read back) |
| works with only the extension's origin in `cors_origins` | CI: `extension.mjs` (second server without it shows the CORS help), `TestCORS` |

## 0.0.16 — Android

| criterion | check |
|---|---|
| one photo → URL in the clipboard within seconds | CI: `android/scripts/emulator-test.sh` (clipboard read back); manual on the owner's phone, 2026-11-20 |
| 5 photos → one collection | CI: emulator test; manual on device, 2026-11-20 |
| airplane mode mid-upload, then off → completes | CI: emulator test (nothing re-sent); manual on device, 2026-11-20 |
| HEIC displays via the server | CI: emulator test |

## 0.0.17 — ACDSeeN

"adseen" turned out to be ACDSeeN, a desktop image viewer without a
database, so there are no records to store links back into (see
`CHANGELOG.md`). Adapted criterion: uploads from it carry `source=acdseen`
and are listed by `GET /api/v1/items?source=acdseen`.

| criterion | check |
|---|---|
| upload from the viewer yields a link (one) or collection link (several) | ACDSeeN's own tests (`tests/test_tibi.py`); manual, 2026-11-20: against a real tibi server |
| `?source=acdseen` lists them | `TestListPagination` covers `?source=`; manual, 2026-11-20 |
