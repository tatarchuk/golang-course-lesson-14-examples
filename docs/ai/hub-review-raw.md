<!-- Verbatim reply to the slide 42 prompt (step 4 of the practical part: an AI review of the hub's concurrency), produced on 2026-10-30 by a fresh Claude Code subagent (general-purpose, Claude Opus 5.5) that saw only the prompt: the four files of the hub as the slides show it (commit dd834ed), line-numbered. Prompt: prompts.txt, "Slide 42". Triage and fixes: README.md, section "Lesson 14 Report: WebSocket Examples". -->

I reviewed only the code in your message. I could not see `onMessage` (the slide-40 chat), `auth`, or docs/slides/tests-as-on-slides.txt, so the findings that depend on them are marked as conditional.

## Answers to your three questions

1. **State used outside the hub goroutine:** `h.clients` is safe. Only `Run` touches it. `h.cfg` is read by handlers (sse_handler.go:17) but never written after `NewHub`, so that is fine too. The real problem is the `*websocket.Conn`: two goroutines write to it (client.go:45 and client.go:74). `c.topics` is safe in the code shown: it is filled before the `register` send, and that send orders it. It is fragile, though (M3).
2. **Exit paths on disconnect:** no. The ping goroutine (client.go:43-47) and the SSE handler (sse_handler.go:19-22) never exit. `readPump` and `writePump` exit only while the hub is responsive, and hub.go:78 can freeze the hub. That includes a deadlock with the client that is disconnecting.
3. **Closing `c.send` twice:** this cannot happen today. hub.go:73 is the only close, and `unregister` is sent once per WebSocket client (client.go:53). SSE clients have the opposite bug: their `send` is never closed. But hub.go:72-73 has no membership check. The usual fix for hub.go:78 (drop the slow client and close its channel), or any second unregister path, makes it panic with "close of closed channel" inside `Run`. Nothing recovers that panic, so the whole process exits.

## Critical

**C1. client.go:45 and client.go:74: two goroutines write to one `websocket.Conn`.**
- The ping goroutine (started at client.go:43) calls `WriteMessage(PingMessage)`. At the same time `writePump` (started at ws_handler.go:42) calls `WriteMessage(TextMessage)`.
- gorilla allows only one writer at a time. Only `Close` and `WriteControl` are safe to call concurrently.
- The result is a data race on the shared write buffer, corrupted frames, and gorilla's panic "concurrent write to websocket connection".
- Both goroutines were started with `go`, so net/http does not recover the panic. The whole server exits.
- Fix: keep a single writer. Either move the ticker into a `select` in `writePump`, or send pings with `conn.WriteControl(websocket.PingMessage, nil, deadline)`.

**C2. hub.go:78: the hub goroutine sends to `client.send` and blocks.**
One client with a full buffer (it reads slowly, its TCP connection is half-open, or its `writePump` is stuck) freezes `Run`. While it is frozen:
- Nothing registers. New connections hang at ws_handler.go:40 (already upgraded at :30) or at sse_handler.go:18.
- Nothing unregisters. Every `readPump` that is disconnecting hangs at client.go:53.
- `Stats` (hub.go:97) hangs, so the debug endpoint cannot report the problem.
- `Publish` (hub.go:91) hangs once 256 events are queued. So do the HTTP handlers that call it, and any `onMessage` that calls it.
- `Stop` does nothing, because `Run` never gets back to its `select`.

It also deadlocks with the slow client itself:
- The hub waits for client X's buffer to drain.
- X's `writePump` is blocked in `WriteMessage`, which has no write deadline (client.go:74).
- Only `c.conn.Close()` at client.go:54 would release that write. It runs after `unregisterClient` at client.go:53, and that call waits for the hub.

This clears only when the kernel drops the TCP connection. If the peer keeps acknowledging zero-window probes (a client that simply stops reading), that never happens. One such client freezes the hub for everyone.

Fix: send without blocking (`select` with `default:` that deletes the client and closes its channel, as in gorilla's chat example). Apply it together with M1.

## High: goroutines with no exit path

**H1. client.go:42-47: the ping goroutine never ends.**
- `for range ticker.C` has no exit, the ticker is never stopped, and the error from client.go:45 is ignored.
- Every connection the server has ever served leaves one goroutine and one ticker behind, and each keeps the conn and its buffers alive. `Stats.Goroutines` only grows.
- Moving the ticker into `writePump` with `defer ticker.Stop()` (the C1 fix) also fixes this.

**H2. sse_handler.go:18-22: the SSE handler never ends on disconnect.**
- There is no `select` on `r.Context().Done()`, the errors from `Fprintf` and `Flush` are ignored, and the handler never unregisters. So `c.send` is never closed and the `range` never ends.
- The request goroutine leaks. The client stays in `h.clients` forever: the hub keeps sending to it and `Stats.Clients` only grows.
- `http.Server.Shutdown` never sees these connections go idle, so it waits until its context expires.
- A half-open SSE client also blocks in `Fprintf`/`Flush` because there is no write deadline. Its `c.send` fills up and that triggers C2.
- Fix:
  - `select` on `ctx.Done()` and on `c.send`, and check `ok` on the receive.
  - Return on a write error.
  - `defer h.unregisterClient(c)`.
  - Limit each write, for example with `http.NewResponseController(w).SetWriteDeadline`. `ctx.Done()` does not interrupt a write that is already blocked.
- The deferred unregister is only deadlock-free after C2 is fixed. Once the handler stops draining `c.send`, the hub may be blocked on this same client.

**H3. client.go:71-77: `writePump` has no exit of its own.**
- It ends only when the hub closes `c.send`.
- `Config.WriteWait` (hub.go:25) is never used, so there is no write deadline.
- The error at client.go:74 is ignored, so it blocks forever on a dead peer, or keeps writing uselessly to a broken one.
- Fix: call `SetWriteDeadline` before every write and return on error. The deferred `Close` then makes `readPump` fail and unregister.

**H4. client.go:52-55: the deferred unregister in `readPump` can block forever.**
- It is an unbuffered send (hub.go:112). It blocks for good if `Run` is stuck (C2) or has returned (`Stop`).
- `c.conn.Close()` waits behind it, so `writePump` is never released either.
- Fix: add a `case <-h.quit:` to the send (M2). Once C2 is fixed the hub always receives `unregister`, and the order of the two calls stops mattering. If C2 is not fixed, close the conn first.

## Medium

**M1. hub.go:72-73: `close(c.send)` without a membership check.**
- Today this is safe only because there is one close site and one unregister per client.
- It becomes a "close of closed channel" panic in `Run` (no recover, so the process exits) in either case:
  - the hub starts dropping slow clients (the C2 fix);
  - a second path sends `unregister` for the same client (for example `writePump` on a write error, or SSE unregistering on both `ctx.Done()` and a write error).
- Fix: delete and close only `if _, ok := h.clients[c]; ok`.

**M2. hub.go:91, 97, 103, 107, 112: the hub API after `Stop()`.**
- After `Stop`, `Run` is gone but the other methods do not know it:
  - `registerClient` blocks forever, and a new WebSocket stays upgraded but dead.
  - `unregisterClient` blocks forever.
  - `Stats` blocks.
  - `Publish` blocks once the buffer is full.
- Existing clients are never released: their `send` is never closed, so every `writePump` and SSE handler leaks. goleak in tests will catch this.
- Each of these sends needs a `case <-h.quit:` branch. `registerClient` could then really return `false`, and ws_handler.go:40 and sse_handler.go:18 should check it (today both ignore it).
- Calling `Stop()` twice panics at hub.go:103. Wrap it in `sync.Once`.
- The comment at hub.go:37 ("drop the event into the queue and move on") is only true while the queue has room. With 256 events queued, `Publish` blocks.

**M3. client.go:65 (conditional): what `onMessage` might do.**
`readPump` passes the `*Client` to `onMessage`. The chat handler is in the same package, so it can touch unexported fields. If it does any of the following, there is a bug:
- Adds or removes entries in `c.topics`: this races with the hub's reads at hub.go:70 and :74 (`String`) and hub.go:77 (`subscribedTo`). The result is a fatal "concurrent map read and map write" error, which cannot be recovered.
- Sends on any client's `c.send`: the hub is then no longer the only sender. Its close (hub.go:73, or the C2 fix that drops slow clients) can hit a send in progress and panic with "send on closed channel".
- Writes to `c.conn`: that adds a third writer, which is C1 again.

Topic changes and replies must go through the hub.

## Low

- **hub.go:96 and :82:** `reply` is unbuffered. That is fine with the current `Stats()`. But if a caller ever gives up (timeout or context), the hub blocks at :82 forever. Use `make(chan Stats, 1)`.
- **hub.go:91:** every subscriber's writer reads the same `Payload` slice at the same time. Read-only use is fine. Callers must not reuse or change the slice after `Publish`, so document that or copy the slice.

## Order of the fixes

- Apply C2 and M1 together. Dropping slow clients without the membership check brings in the double close.
- Apply the unregister in H2 and the early return in H3 only after C2. Otherwise a goroutine that stops draining `c.send` can deadlock with the hub.
- C1 and H1 are one change: move the ticker into `writePump`.
- Validate the config these fixes depend on:
  - `SendBuffer > 0`: with an unbuffered `send`, a hub that never blocks drops every client.
  - `0 < PingPeriod < PongWait`: `time.NewTicker` panics at client.go:42 if the period is not positive.
  - `WriteWait > 0`.

## Not concurrency, but I noticed

- `Config.MaxMessageSize` (hub.go:26) is never applied. There is no `conn.SetReadLimit`, so a client can send messages of any size.
- **sse_handler.go:15:** `w.(http.Flusher)` is not checked and panics when middleware wraps the writer. Use the two-value form or `http.NewResponseController(w).Flush()`.
- `conn.Close()` runs three times (ws_handler.go:35, client.go:54, client.go:72). This is harmless: gorilla's `Close` is safe to call concurrently and more than once.
