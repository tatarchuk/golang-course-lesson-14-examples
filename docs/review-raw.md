<!-- Final review of Lesson 14 (examples): the answer of a fresh read-only subagent (Claude Fable 5.1) to the
last prompt in prompts.txt, verbatim. The triage is in the README, section "Фінальний огляд". -->

# Review: golang-course-lesson-14-examples (Lesson 14, WebSocket examples)

Repository: `/Users/oleksandr.tatarchuk/repo/golang-course-lesson-14-examples`, branch `main`, range `1dbae80..5a0d57f` (10 commits, 59 files in the net diff; the review package and the tree diff have identical file sets). Paths below are relative to that root. Review done read-only; the only writes were `bin/` (built by `scripts/smoke.sh`) and files in the session scratchpad. The diff was reviewed in passes: final tree file by file, then `git show` of the as-on-slides commit `0e85619`, the fix commits `d59ecca`/`add6761`, and the scaffold `1dbae80`.

### Strengths

- **The slide-first, fix-later story is real and reproducible.** `0e85619` is the slides' code (blocking `client.send <- msg`, a separate ping goroutine, `range c.send` SSE, room from the message body, bare `Stop`); the eight `t.Skip(asOnSlides)` lines are added there and removed in `d59ecca`; `make build-as-on-slides` rebuilds that commit into `bin/libratrack-as-on-slides`, so the +300 vs +0 comparison in `docs/slides/cycles.txt` can be rerun by anyone.
- **The fixed hub is sound on every slide 42 / slide 45 question I walked.** `h.clients` is touched only by `Run`/`drop` (`internal/realtime/hub.go:94-137`, enforced by CI); `drop` is the only `close(c.send)` and is guarded by membership (`hub.go:130`), so late unregisters are no-ops; `done` (closed when `Run` returns) unblocks `Publish`, `Stats`, `registerClient`, `unregisterClient` after `Stop` (`hub.go:142-184`); `Stop` is `sync.Once` + wait; fan-out is non-blocking with a 1008 drop (`hub.go:108-112`); the close code is chosen by the hub and written by the single writer after the queued frames drain (`client.go:89-96`, channel-close ordering makes `closeCode` race-free); every pump has an exit path (read error / deadline, closed `send` / failed write) and each pump's `Close` unblocks the other.
- **Tests prove what their names say, with real sockets.** Byte-exact slide 8 handshake, deadlines on every read, `expectSilence` for "no duplicate", `closeWithin` turns a hung handler into a failure instead of a hung run, the M1 double-close panic was recorded before the guard was added (`docs/slides/tests-after-review.txt:10-23`). No vacuous passes found; `-count=5` on the two timing-sensitive packages was green for me as `-count=10` was for the implementer.
- **CI encodes the deck's checklists** (single owner, one writer, `CheckOrigin` never `return true`, `racedemo` tag, JWT guard, ASCII-only Go) and the `race-demo` job requires both a failing run *and* the `WARNING: DATA RACE` text (the Ubuntu job log says "reported 3 data races").
- **Security hygiene within the stated scope:** exact-origin `CheckOrigin`, HS256-only `Parse` with required expiry, the server never logs a URL (gorilla's handshake errors carry no URL), pages render only through `textContent`, token per tab in `sessionStorage`, no JWT in docs/screenshots (S3 checked), only the gmail author in history, no attribution trailers.
- **Documentation discipline:** every number in the README resolves to a file under `docs/` (table below); the AI replies are verbatim with the prompt reconstruction recipe; `cmd/wsclient` is a reusable load tool that uses `DisableKeepAlives` so its own calls do not pollute the goroutine count.

### Verification run

Mac, `go version go1.27.1 darwin/arm64`, working tree clean before and after (`git status --short` empty).

| Command | Result |
|---|---|
| `gofmt -l .` | no output, exit 0 |
| `go vet ./...` and `go vet -tags racedemo ./...` | exit 0 |
| `go mod tidy -diff` (read-only form) | empty diff, exit 0 |
| `go test -race -count=1 -v ./...` | exit 0; **26 PASS, 0 FAIL, 0 SKIP** (auth 3, library 3, mutexhub 2, realtime 5, server 13) |
| `go test -race -count=5 ./internal/realtime ./internal/server` | ok, ok (7.9 s for server) |
| `go test -race -count=1 -tags racedemo ./internal/naive` | **exit 1, 3 `WARNING: DATA RACE` reports** at `clients.go:23`, `:24` (x3), `:34` (x2) |
| `go test -count=1 -tags racedemo ./internal/naive` (no `-race`) | ok, exit 0 (the slide 44 trap) |
| `bash scripts/smoke.sh` (builds `bin/`, server on 127.0.0.1:18090) | **15/15 PASS, `SMOKE OK`, exit 0** |
| CI static greps replayed locally (single owner, one writer, no `return true`, racedemo tag, ASCII, JWT guard, `.env`, `bash -n`, no root `.go`) | all PASS |
| `gh run view 41047094735` | green: Static 17 s, Race tests and smoke 36 s, Race detector catches slide 23 37 s |
| Extra: 6 x `kill -INT` of `bin/libratrack` with one `wsclient listen` connected | 6/6 clients saw `close 1001 (going away): server shutdown` |
| Extra: `-pong-wait 2s`, a reading `wsclient listen` for 7 s | still registered at t=7 s (keepalive works end to end) |

### Issues

#### Critical (Must Fix)

None.

#### Important (Should Fix)

**I1. `race.log` is a tracked file at the repository root** (added in `5a0d57f`, 191 lines). It is the output of replaying the CI `race-demo` job locally (the job redirects `go test` into `race.log` in the working directory), it embeds absolute `/Users/oleksandr.tatarchuk/...` paths, nothing references it (README, Makefile, CI required list), and every future local replay of that job will dirty the tree. Fix: `git rm race.log`, add `race.log` (or `*.log`) to `.gitignore`, and point the workflow at `"$RUNNER_TEMP/race.log"`. Proof: a static check `! git ls-files | grep -E '\.log$'`, or simply the required-files list staying as is plus a clean `git status` after `make race-demo`.

#### Minor (Nice to Have)

**M1. `cmd/libratrack/main.go:50,68`: shutdown relies on a callback net/http does not wait for.** `srv.RegisterOnShutdown(s.Hub.Stop)` runs `Stop` in a goroutine; `Shutdown` returns as soon as the listener and the non-hijacked connections are gone (the net/http docs: it "does not attempt to close nor wait for hijacked connections ... the caller should ... wait for them to close"), and `main` then returns. The 1001 frames therefore race the process exit. Empirically 6/6 on this 12-core Mac, so no defect today, but the README line 24 promises 1001 on graceful stop and nothing holds it on a slower box. Fix: after `srv.Shutdown` returns call `s.Hub.Stop()` (idempotent, waits for `Run`), and let `Stop` also wait for the pumps (a `sync.WaitGroup` incremented in `ServeWS` before `go c.writePump()`, `Done` in its defer, bounded by `WriteWait`). Proof: `TestStopWaitsForCloseFrames`: after `Stop()` returns, each client reads 1001 with a 100 ms deadline.

**M2. README triage row H3 names a test that does not exercise the fix.** "обірвані цикли в `TestConnectDisconnectCyclesNoLeak`": with an abrupt TCP close `readPump` gets EOF, the hub closes `send`, and `writePump` exits through the closed channel (`client.go:89-96`); no write ever fails, so the write deadline / return-on-error path of `client.go:97-104` is untested. The fix is present; the proof is not. Either mark the row "no test" or add one: a client that never reads while the hub publishes 32 KiB payloads with `WriteWait` 200 ms until the write blocks; the client must be unregistered within ~1 s.

**M3. No test proves the keepalive of slide 15.** `TestDeadPeerTimesOut` would still pass if `SetPongHandler` (`client.go:62-64`) were deleted (every client would then die after PongWait). Suggested `TestHealthyClientSurvivesPongWait`: PongWait 300 ms, PingPeriod 100 ms, a client that reads; still registered after 1 s. (Verified manually above; see the verification table.)

**M4. No socket-level test asserts the 1008 close frame.** `TestSlowClientDroppedNotBlocking` checks the channel close only; the only 1008 evidence is a server log line in `tests-after-review.txt`. Suggested: `tune` SendBuffer 2, a dashboard client that does not read, 5 publishes, then the client reads: two data frames then close 1008.

**M5. `internal/realtime/hub.go:38-50`: `Validate` accepts an empty `AllowedOrigin`.** With `-origin ""` the exact comparison in `ws_handler.go:21` is true for every request *without* an Origin header (curl, `wsclient -origin ""`), which is the "accept everything from non-browsers" shape slide 16 warns about (browsers would all be rejected instead). One more `case` plus an entry in `TestNewHubRejectsBadConfig`.

**M6. `scripts/smoke.sh:53-55`: the dashboard-event check depends on a fixed `sleep 1`** for `wsclient listen` to log in and connect before the REST call. On a slow runner the event could be published before the client is registered and the check "the dashboard client received loan_returned L-2" would fail spuriously. Poll `/debug/stats` for `"clients":1` as `scripts/dead-peer.sh:30-35` already does.

**M7. `internal/realtime/client.go:68`: server-initiated drops are logged as a network failure.** After the hub logs "unregister ..., server shutdown" (or "slow consumer"), `readPump` logs "disconnected: connection lost: read tcp ...: use of closed network connection" (seen in my shutdown runs). `errors.Is(err, net.ErrClosed)` -> "closed by the server" keeps the slide 16 log honest without touching `closeCode` from the reader goroutine.

**M8. README wording vs. evidence (two spots).** (a) Line 146: "15 перезавантажень і переходів" counts the 15 `hub: register` lines of `browser-cycles.txt`, one of which (12:43:12) is bohdan's SSE auto-reconnect; anna's reloads/navigations are 14 (11 chat + 3 dashboard). (b) Line 187: the slide 42 review "знайшов усі вісім проблем, які показали тести" contradicts line 169 ("Кімнату з повідомлення клієнта огляд не бачив"): seven of eight.

**M9. Evidence hygiene.** `docs/slides/tests-before.txt:37-39` keeps absolute `/Users/...` paths while `naive-race.txt` was relativised; `docs/environment.txt` lacks the `go list -m github.com/golang-jwt/jwt/v5` line that plan step 1.1 asks for (the README's "v5.3.1" is backed by `go.mod` only).

**M10. Test nits.** `internal/server/helpers_test.go:49-52`: the `recover()` around `Hub.Stop()` and its comment are leftovers from the as-on-slides hub (Stop is idempotent now). `TestHandshakeRequiresToken` uses a malformed token (`not.a.token`) where the plan asked for "a token signed with another secret"; that case is covered only at unit level by `TestParseRejectsOtherSecretAndExpiredToken`.

### Numbers checked

| Claim | Where it appears | Source checked | Match? |
|---|---|---|---|
| 14 of 22 tests pass, 8 fail on the slide code | README step 3, "Докази" | `docs/slides/tests-as-on-slides.txt:5,12-33` (14 PASS, 8 FAIL lines) | yes |
| healthy client got 3 of 10 | README table (slow client) | `tests-as-on-slides.txt:19` | yes |
| second `Stop` panics "close of closed channel" | README table | `tests-as-on-slides.txt:20` | yes |
| 3-6 races + gorilla "concurrent write" panic in each of 3 runs | README table (one writer) | `tests-as-on-slides.txt:7-8,31` (3, 6, 5 races) | yes |
| ~100 extra goroutines after 100 connections | README table (leak) | `tests-as-on-slides.txt:32` (104, baseline 4) | yes |
| eight tests skipped before the fixes | README step 3 | `git grep -c t.Skip 0e85619`: 2 (hub) + 6 (server) | yes |
| 26 of 26 with `-race`, 10 runs in a row | README step 4, "Докази" | `tests-after-review.txt:35-84` (26 PASS lines, `-count=10`); my run 26/0/0 | yes |
| S6: 18 hub+server tests, 8 of them failed before | README S6 | 5 realtime + 13 server = 18; 2 + 6 = 8; `s06-tests-after-review.png` | yes |
| M1 test panics without the membership check | triage M1, "Докази" | `tests-after-review.txt:10-23` | yes |
| send queue 16 -> 64 -> 256; 20 messages overflow 16; 64 is the slide 34 value; 256 = gorilla's chat example | "Черга відправки" | `tests-after-review.txt:4,25-31`; `add6761`; `docs/ai/chat-design-raw.md:49,82,322`; gorilla `examples/chat` uses 256 | yes |
| +300 goroutines per 3x100 cycles (slide code), one ping goroutine per connection | README S7, step 3, step 5, line 54 | `run-as-on-slides.txt:31-36` (9 -> 309); `cycles.txt:16-27` (7 -> 307 -> 607); `s07-cycles.png` | yes |
| 207 goroutines with 100 clients (2 pumps + 7), back to 7 (+0), close frame and abrupt | README S7, step 5 | `cycles.txt:33-43` | yes |
| 302 "unexpected close" lines per run (slide 14) | "Що не спрацювало" | `run-as-on-slides.txt:58-60` | yes |
| dead peer dropped in 2-3 s at pong wait 3 s | README S8, step 5 | `dead-peer.txt:8-15` (STOP 12:37:55, unregister 12:37:57, "no pong before the read deadline") | yes |
| 101 + `Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=`, foreign Origin 403, no token 401 | README S3, "Докази" | `handshake.txt:12-15,20,31`; `s03-handshake.png` (no token visible); test + smoke | yes |
| 15 reloads/navigations, 0 clients, 7 goroutines, every close 1001, 3 "suspension" | README step 5 | `browser-cycles.txt:9-12,64-66`: 15 registers of which 1 SSE; 14 client closes all 1001; 3 suspension | 14 vs 15 (M8a) |
| smoke 15 of 15 | README step 5, "Докази" | `smoke.txt` (15 PASS); my run 15 PASS | yes |
| three races exactly at the three "race!" lines; passes without `-race`; mutexhub clean | README S2, step 1 | `naive-race.txt` (reports pair lines 34/23, 24/34, 24/24; `go test` ok; mutexhub ok); my run 3 reports; CI job log "3 data races" | yes |
| macOS 27.2 arm64 M2 Pro, Go 1.27.1, gorilla v1.5.3, jwt v5.3.1, Chrome 156, Safari 27.2 | README "Середовище", S1 | `docs/environment.txt` (all but jwt); `go.mod` (jwt v5.3.1) | yes (jwt from go.mod, M9) |
| mutexhub test with 50 goroutines | README structure | `internal/mutexhub/hub_mutex_test.go:15` | yes |
| close codes 1001 shutdown, 1008 slow, 1009 too big | triage, structure, checklists | `hub.go:111,118`; `client.go:60` + gorilla; `TestShutdownClosesClients`, `TestOversizedMessageCloses1009` | yes (1008 untested on a socket, M4) |
| PongWait 60 s, ping 54 s, WriteWait 10 s, read limit 4096, buffers 1024/1024, SendBuffer 256 | plan constraints, README | `cmd/libratrack/main.go:35-42`, `ws_handler.go:18-19` | yes (256 vs plan's 16: documented deviation) |
| smoke server on 127.0.0.1:18090; cycles 3x100 + 3x100 | README lines 43, 54 | `scripts/smoke.sh:10`, `scripts/cycles.sh:22-23` | yes |
| S4 three users' messages once each; S5 L-3, L-4 204 then 409 | README S4, S5 | `s04-chat.png`, `s05-dashboard-sse.png` | yes |
| Makefile 6 targets, CI 3 jobs, 5 scripts, 8 screenshots, 11 evidence files | README structure, "Докази" | `Makefile`, workflow, `scripts/`, `docs/` | yes |
| slide 42 review "found all eight problems the tests showed" | README "ШІ на кожному кроці" | `hub-review-raw.md` covers C1, C2, H1, H2, H4/M2, M1, SetReadLimit; room-from-message not covered (README line 169 says so) | 7 of 8 (M8b) |
| triage: each "fixed" row has its code change and its test | triage table | C1 `client.go:100-104`; C2 `hub.go:108-112`; H1 `client.go:84`; H2 `sse_handler.go:24,34-48`; H3 `client.go:90-104`; H4 `hub.go:179-184`; M1 `hub.go:130`; M2 `hub.go:70-73,162-175`; Low `hub.go:151,139-141`; Validate `hub.go:38-50`, `main.go:43`; read limit `client.go:60`; `NewResponseController` `sse_handler.go:17`; all named tests exist and pass | yes, except H3's proof (M2) |

### Declined to judge

- Chat text validation (trim, 1-1000 runes, `error` frames): the app repository's contract; the examples bound frames by the 4096-byte read limit only, as slide 40 does.
- `GET /debug/stats` and `GET /api/loans` without authentication: the plan's route list defines them so for a local demo.
- Token in the query string and in `sessionStorage`, demo login without a password: slide 39 scope accepted by the plan; the AI's advice against it is recorded in the README.
- No TLS / `wss://`, per-user connection limits, rate limiting, Redis fan-out: the plan's "Out of scope".
- `Hub.Stop` blocking forever when `Run` was never started: documented on the method; both the server and every test start `Run`.
- A burst larger than `SendBuffer` dropping a healthy client: inherent to the non-blocking policy of slide 28; the README documents the sizing.
- `EventSource` reconnecting forever against a stopped server: browser behaviour.
- `.idea/` and `*.iml` present in the working tree: ignored, not tracked (memory `idea-empty-project-view`).
- `make build-as-on-slides` failing on a shallow clone: a local target, CI does not run it.
- `cmd/wsclient/main.go:216` ignoring the error of the mid-cycle `serverStats()`: cosmetic in a demo tool.
- Events queued in `broadcast` when `Run` returns on `Stop` are discarded: acceptable at shutdown.
- `actions/checkout@v4` / `setup-go@v5` Node 20 deprecation annotations: GitHub housekeeping, runs still pass (see Recommendations).

### Recommendations

1. Fix I1 before the final push (two lines plus the workflow path); it is the only change the deliverable needs.
2. Plan deviations to confirm as intentional (all harmless, most already documented): SendBuffer 256 instead of 16 (README explains); no `make smoke` target (the plan lists one, the README does not claim it); `smoke.sh` on 18090 instead of the plan's 18080; dead-peer run with pong wait 3 s instead of 10 s; `cycles.sh` 3 cycles instead of the plan's 5; 14/15 browser rounds instead of 20; the CI single-owner/one-writer greps include `_test.go` (stricter than the plan's "excluded").
3. Add the three cheap tests of M3, M4 and the H3 proof of M2 (or relabel the H3 row); together they close the only slide 15/28 behaviours that currently rest on manual evidence.
4. Consider the `Stop`-waits-for-pumps change of M1 in both repositories; the app repository uses the same `RegisterOnShutdown(hub.Shutdown)` pattern per the plan, so the same contract gap exists there.
5. Housekeeping when convenient: bump `actions/checkout` and `actions/setup-go` to the Node 24 majors; relativise the paths in `tests-before.txt`; add the jwt `go list -m` line to `environment.txt`.

### Assessment

**Ready to merge? With fixes.** **Reasoning:** the implementation matches the plan's interfaces, tests, routes and constraints, the hub is correct on every concurrency question of slides 42 and 45, and every number in the README traces to evidence (two wording slips aside); the only change needed is removing the accidentally committed `race.log` (I1), with the rest being coverage and wording improvements.
