<!-- Verbatim reply to the slide 34 prompt (designing a real-time chat architecture with AI), produced on 2026-10-30 by a fresh Claude Code subagent (general-purpose, Claude Opus 5.5) that saw only the prompt. Prompt: prompts.txt, "Slide 34". How it compares with the examples and the albums chat: README.md, section "Lesson 14 Report: WebSocket Examples". -->

# LibraTrack staff chat: architecture

I wrote this from your message only and did not read the repository. Please check these assumptions against the code:
- The existing Gin JWT middleware reads `Authorization: Bearer <jwt>`, validates it and puts the claims (user ID, name, department, role, `exp`) into `gin.Context`.
- Storage is PostgreSQL.
- The WebSocket library is `github.com/gorilla/websocket`. The design is the same with `github.com/coder/websocket`.

## 1. Overview

- **Transport:** one WebSocket per room, `GET /api/v1/chat/rooms/:dept/ws`. The room is fixed when the connection opens, and the server never reads a room ID from a chat message. A client therefore cannot post into another department's room, even with a crafted payload.
- **REST endpoints, behind the same middleware:**
  - `GET /api/v1/chat/rooms` lists the rooms the caller may join.
  - `GET /api/v1/chat/rooms/:dept/messages?after_seq=N&limit=50` returns history and is used to catch up.
- **Runtime types in `internal/chat`:**
  - `Hub` keeps the list of rooms and starts and stops them.
  - `Room` is one goroutine per department and the only code that changes its member list.
  - `Client` is one per connection. It has two goroutines (`readPump` and `writePump`) and a bounded outbound queue.

This is the gorilla chat example with two changes: one goroutine per room instead of one global hub, and a safer way to close clients (section 3). A supervisor who follows several departments opens one socket per room. If that becomes common, one socket can carry several room subscriptions without changing `Room`.

```
Browser A --WS GET--> existing JWT middleware --> Connect (room check) --> Upgrade --> room.Join(A)

Browser A --frame--> readPump(A): read limit, decode, validate, rate limit
                        --> store.Save (per-room seq) --> room.Broadcast(payload)
                                                              |
                     Room "circulation" goroutine  <----------+
                     for c := range clients { non-blocking c.send <- payload }
                         |                    |                    |
                     send(A) [64]        send(B) [64]        send(C) full --> Close(1013), delete
                         |                    |
                     writePump(A)        writePump(B)     (each write has a 10 s deadline)
                         v                    v
                     Browser A           Browser B

Room "cataloging": its own goroutine, map and channels; it never sees circulation traffic.
```

## 2. How rooms map to Go data structures

| Domain concept | Go | Who touches it |
|---|---|---|
| Department room | `RoomID` (department ID or slug) | - |
| All rooms | `Hub.rooms map[RoomID]*Room` | `sync.RWMutex`. Filled from the `departments` table at startup. An unknown ID returns 404 |
| Members of one room | `Room.clients map[*Client]struct{}` | Only the room goroutine, so no lock |
| One connection (browser tab or device) | `*Client` | - |
| Messages waiting for one connection | `Client.send chan []byte`, buffer 64 | The room and the client's own `readPump` add to it. Only `writePump` takes from it |
| One message | `[]byte`, JSON-encoded once | Shared, read-only, by all receivers |
| Who is online | `Room.online map[UserID]int` (open connections per user) | Only the room goroutine |

The member map is keyed by `*Client`, not by user ID, so a librarian with two tabs is two clients. A presence event goes out when a user's count in `online` changes from 0 to 1 or from 1 to 0.

```go
type RoomID string // department ID or slug, e.g. "circulation"
type UserID int64

type Hub struct {
	mu    sync.RWMutex
	rooms map[RoomID]*Room
	store MessageStore
	stop  chan struct{}  // closed by Stop
	wg    sync.WaitGroup // room and client goroutines
}

type Room struct {
	id        RoomID
	clients   map[*Client]struct{} // only run() touches it
	online    map[UserID]int       // only run() touches it
	join      chan *Client
	leave     chan *Client
	broadcast chan []byte          // buffered (256), pre-encoded JSON
	kick      chan UserID
	stop      <-chan struct{}      // Hub.stop
}

type Client struct {
	conn      *websocket.Conn
	room      *Room
	user      User          // ID, name, department, role, copied from the JWT claims
	send      chan []byte   // buffered (64); never closed
	done      chan struct{} // closed exactly once, by Close
	closeOnce sync.Once
	closeCode int
	closeText string
	limiter   *rate.Limiter // golang.org/x/time/rate: burst 5, then 1 per second
}
```

## 3. Hub design

`Hub` does little work:
- It maps each department to its room and starts one goroutine per room.
- It creates rooms from the `departments` table at startup, and through `AddRoom` when a department is added. It never creates a room from user input, so the number of rooms stays bounded.
- It runs operations that cover all rooms:
  - `Kick(userID)` sends to every room's `kick` channel. Use it when an admin deactivates a user or moves them to another department.
  - `Stop()` and `Wait(ctx)` handle shutdown.

Each `Room` is an actor: a single loop makes every membership change and does every fan-out, so the map needs no lock. Giving each room its own goroutine keeps rooms separate: a busy Circulation room cannot delay Cataloging. A `sync.RWMutex` around the maps would also work. The actor version is preferred because the loop itself guarantees that fan-out never blocks, instead of relying on every caller to follow a rule.

```go
func (r *Room) run() {
	for {
		select {
		case c := <-r.join:
			r.clients[c] = struct{}{} // + presence event on the user's first connection
		case c := <-r.leave:
			delete(r.clients, c) // safe to repeat: the client may already be evicted
		case msg := <-r.broadcast:
			for c := range r.clients {
				if !c.Enqueue(msg) {
					delete(r.clients, c) // deleting during range is safe in Go
				}
			}
		case uid := <-r.kick:
			for c := range r.clients {
				if c.user.ID == uid {
					c.Close(websocket.ClosePolicyViolation, "access revoked")
					delete(r.clients, c)
				}
			}
		case <-r.stop:
			for c := range r.clients {
				c.Close(websocket.CloseGoingAway, "server shutting down")
			}
			return
		}
	}
}
// Join, Leave and Broadcast are channel sends inside a select on r.stop,
// so no caller can hang after shutdown.
```

```go
// Enqueue never blocks. A full queue means the client is too slow, so it is closed.
func (c *Client) Enqueue(msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		c.Close(websocket.CloseTryAgainLater, "too slow, reconnect")
		return false
	}
}

// Close is safe to call from any goroutine, any number of times.
func (c *Client) Close(code int, text string) {
	c.closeOnce.Do(func() {
		c.closeCode, c.closeText = code, text
		close(c.done)
	})
}

func (c *Client) writePump() {
	ping := time.NewTicker(pingPeriod)
	defer func() {
		ping.Stop()
		c.conn.Close() // also unblocks readPump
	}()
	for {
		select {
		case msg := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			frame := websocket.FormatCloseMessage(c.closeCode, c.closeText)
			c.conn.WriteControl(websocket.CloseMessage, frame, time.Now().Add(writeWait))
			return
		}
	}
}

func (c *Client) readPump(store MessageStore) {
	defer func() {
		c.room.Leave(c)
		c.Close(websocket.CloseNormalClosure, "")
	}()
	c.conn.SetReadLimit(maxFrame) // 8 KiB; a larger frame closes the socket with 1009
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return // the peer closed, the pong timed out, or writePump closed the connection
		}
		in, err := decodeSend(data) // client_msg_id, body (1-2000 chars)
		if err == nil && !c.limiter.Allow() {
			err = errRateLimited
		}
		if err != nil {
			c.Enqueue(errorFrame(err)) // to this client only
			continue
		}
		msg, err := store.Save(ctx, c.room.id, c.user.ID, in) // ctx: per-call timeout; assigns seq
		if err != nil {
			c.Enqueue(errorFrame(err))
			continue
		}
		c.room.Broadcast(encodeMessage(msg, c.user)) // encoded once for all receivers
	}
}
```

**Why `send` is never closed.** In the gorilla example the hub closes `client.send`. That is safe only while the hub is the only goroutine that sends on it. Here `readPump` also sends to its own client (error frames). If the room closed the channel, that send could panic with "send on closed channel". A `done` channel closed through `sync.Once` lets any goroutine call `Close`, any number of times. It also means a `Client` can later belong to several rooms, because no room owns its channel.

## 4. JWT authentication at connect

A WebSocket connection starts as an ordinary `GET` request. The existing middleware therefore runs before the handler, unchanged. The handler then returns 404 for an unknown room and 403 for another department's room. Both are plain HTTP responses, sent before any socket exists.

```go
func (h *Handler) Connect(c *gin.Context) {
	claims := auth.ClaimsFrom(c) // stored by the existing middleware
	room, ok := h.hub.Room(chat.RoomID(c.Param("dept")))
	if !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if !chat.CanJoin(claims, room.ID()) { // own department, or admin
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade has already written the HTTP error
	}
	client := chat.NewClient(conn, room, chat.UserFrom(claims)) // plain values, not c
	t := time.AfterFunc(time.Until(claims.Expiry()), func() { client.Close(4001, "token expired") })
	defer t.Stop()
	room.Join(client)
	go client.writePump()
	client.readPump(h.store) // runs until the socket closes
}
```

Points to get right:

- **Browsers cannot set headers on `new WebSocket()`.** Native clients and Go tests can send `Authorization`, so they need nothing extra. For browsers, add a small adapter in front of the existing middleware:
  - The client opens `new WebSocket(url, ["libratrack.chat.v1", "bearer." + jwt])`.
  - The adapter copies the token into `Authorization`, and the existing middleware still does all the validation.
  - Set `Upgrader.Subprotocols = []string{"libratrack.chat.v1"}`. The server then sends back only that value, never the token.
  - Do not put the JWT in the query string, because proxies and access logs record URLs. If a query parameter cannot be avoided, use a single-use ticket that expires after 30 seconds, issued by a REST endpoint.

  ```go
  // In front of the existing middleware; does nothing for other requests.
  func bearerFromSubprotocol() gin.HandlerFunc {
  	return func(c *gin.Context) {
  		if websocket.IsWebSocketUpgrade(c.Request) && c.GetHeader("Authorization") == "" {
  			for _, p := range websocket.Subprotocols(c.Request) {
  				if tok, ok := strings.CutPrefix(p, "bearer."); ok {
  					c.Request.Header.Set("Authorization", "Bearer "+tok)
  					break
  				}
  			}
  		}
  		c.Next()
  	}
  }

  api := r.Group("/api/v1", authMiddleware) // existing
  api.GET("/chat/rooms", chatH.ListRooms)
  api.GET("/chat/rooms/:dept/messages", chatH.History)
  r.GET("/api/v1/chat/rooms/:dept/ws", bearerFromSubprotocol(), authMiddleware, chatH.Connect)
  ```
  The WebSocket route is registered outside the group so that the adapter runs before the middleware. If the middleware is installed globally with `r.Use`, install the adapter globally before it.
- **`CheckOrigin`:** allow only the LibraTrack frontend origins. An empty `Origin` means a non-browser client and can be allowed. Never `return true`: this check is what stops cross-site WebSocket hijacking.
- **Token expiry on a long-lived socket:** the JWT is checked once, when the connection opens. The `AfterFunc` above closes the socket with code 4001 when the token expires. The client then refreshes the token and reconnects. If access is revoked or the department changes, call `hub.Kick(userID)`.
- **The server sets identity:** author ID and name come from the claims, and the room comes from the connection. The payload carries only the text and a client message ID.
- **Do not use `*gin.Context` in goroutines.** Gin reuses contexts, so copy the values first.
- **Browsers hide the HTTP status of a failed connect.** JavaScript sees only close code 1006. The frontend should refresh the token before it connects. After a failed connect, it can call a cheap REST endpoint to tell a 401 from a network error.

## 5. Message flow

1. **Connect.** The middleware validates the JWT. `Connect` checks the room and the department, then calls `Upgrade` and `room.Join(client)`. `writePump` starts, and `readPump` runs in the handler goroutine until the socket closes. If this is the user's first connection, the room sends a presence event.
2. **Catch up.** The client opens the socket first, then calls `GET .../messages?after_seq=<last contiguous seq>` and merges the results by `seq`. Because the socket opens first, nothing is lost between history and live traffic. Duplicates are dropped by `seq`.
3. **Send.** The client sends `{"type":"send","client_msg_id":"<uuid>","body":"Returns cart is at desk 2"}`.
4. **Validate (`readPump`).**
   - Frames are limited to 8 KiB.
   - The body must be 1 to 2000 characters.
   - The rate limiter applies.
   - A rejection goes back as an `error` frame to this client only.
5. **Persist.** `store.Save` inserts the row and gets a per-room `seq` in the same transaction: `UPDATE chat_rooms SET last_seq = last_seq + 1 WHERE id = $1 RETURNING last_seq`.
   - The row lock makes inserts into one room run one at a time. `seq` therefore has no gaps and follows commit order. That is fine at staff-chat volume.
   - A unique key on `(author_id, client_msg_id)` makes a resend after reconnect harmless.
   - Database latency delays only this sender's loop.
6. **Publish.** The outbound JSON is encoded once, then `room.Broadcast(payload)` puts it in the room's buffered inbox (256). If the inbox is ever full, the sender waits, never a receiver.
7. **Fan-out.** The room loop calls `Enqueue` for every member. A member whose queue is full gets `Close(1013)` and is removed.
8. **Deliver.** Each `writePump` writes `{"type":"message","room":"circulation","seq":4182,"author":{"id":17,"name":"Olena K."},"body":"...","client_msg_id":"...","sent_at":"2026-10-30T09:14:03Z"}`.
   - The sender also gets its own message back and uses `client_msg_id` to confirm the message it already showed.
   - One sender's messages stay in order, because its `readPump` handles them one at a time.
   - Messages from two senders at once can reach the room in either order. The client inserts by `seq` and fetches history if a gap does not fill quickly.
9. **Disconnect.** A read error, a pong timeout or a close frame ends `readPump`. Its `defer` calls `room.Leave(c)` and `c.Close(...)`. `writePump` then sends a close frame and closes the connection. Every step is safe to repeat, so eviction, kick, token expiry and a normal close can happen at the same moment without harm.

## 6. Not blocking on a slow client

Four rules make this hold by design:
1. The room loop does only map operations and non-blocking channel sends. Its cost per message depends on the number of members, never on any network.
2. Only the client's own `writePump` writes to the network, and every write has a deadline. A stuck peer can stall only its own goroutine, for at most 10 s.
3. No lock is held during I/O. The Hub mutex protects only map lookups.
4. A slow client is disconnected and catches up later. Messages are never silently dropped for it, so there are no hidden gaps, and memory stays bounded.

| Case | What happens | Who waits |
|---|---|---|
| Client stops reading (TCP window full) | Its `writePump` blocks for at most 10 s. Its queue fills, and the room evicts it with 1013. The write deadline then closes the socket | Only that client's `writePump` |
| Client disappears without closing the connection (Wi-Fi drop, laptop lid closed) | No pong arrives within 60 s, so `readPump` exits and calls `Leave` | Nobody |
| Short stall or burst (up to 64 messages) | The queue absorbs it | Nobody |
| Evicted client | Reconnects with increasing, randomized delays and catches up from its last contiguous `seq` | Nobody |
| Client that floods messages | The rate limiter rejects the extra messages. Repeated abuse closes it with 1008 | Only the sender |

Constants:
- `writeWait` 10 s and `pongWait` 60 s.
- `pingPeriod` 30 s. It must be shorter than `pongWait` and shorter than the proxy or load-balancer idle timeout, which is often 60 s.
- Send queue 64, room inbox 256, maximum frame size 8 KiB.

Payloads are shared, so a slow client holds at most 64 slice headers. You can also cap connections per user, for example at 5.

## 7. Operations and scaling

- **Shutdown:** `http.Server.Shutdown` does not track WebSocket connections, because they have been hijacked.
  - Register `srv.RegisterOnShutdown(hub.Stop)`. The callback must only signal, not wait. The rooms then close all clients with 1001.
  - After `srv.Shutdown` returns, call `hub.Wait(ctx)`. It waits on the `WaitGroup` of room and client goroutines, with a timeout.
- **Middleware:** keep middleware that wraps the response (gzip, `gin-contrib/timeout`) off the WebSocket route, because it breaks `Hijack`. Behind nginx, set `proxy_http_version 1.1` and forward the `Upgrade` and `Connection` headers.
- **More than one instance:** the in-memory hub reaches only clients connected to the same process.
  - Put step 6 behind an interface, `Broadcaster.Publish(ctx, room, payload)`. Implement it in-process for now.
  - When LibraTrack runs two or more replicas, switch to Redis Pub/Sub (`chat:room:<id>`) or Postgres `LISTEN/NOTIFY`. With Postgres the payload limit is 8000 bytes, so send `room:seq` and load the row.
  - Each instance runs one subscriber that feeds its local rooms. The sending instance also gets its own message through the subscription, so nothing is delivered twice.
- **Monitoring and privacy:** track `chat_connections{room}`, `chat_messages_total{room}` and `chat_evictions_total{reason}`. Log connect and close events with user, room and close code, but never message text, because staff chat can mention patron data. Set a retention period for `chat_messages`.

## 8. Tests and package layout

Use `httptest.NewServer(router)` and `websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer " + tok}})` to test that:
- a connection without a token gets 401;
- a connection to another department's room gets 403;
- a message sent in `circulation` never reaches `cataloging`;
- with a queue size of 1, a client that never reads is closed with 1013 while another client still receives every message;
- an expired token closes the socket with 4001.

Run the tests with `-race`, and call `goleak.VerifyNone(t)` after shutdown.

```
internal/chat/
  hub.go       Hub: room registry, Kick, Stop, Wait
  room.go      Room actor loop, Join/Leave/Broadcast
  client.go    Client: readPump, writePump, Enqueue, Close
  protocol.go  inbound/outbound message formats, validation
  store.go     MessageStore interface (Postgres implementation in the storage layer)
  handler.go   Gin: Connect, ListRooms, History, bearerFromSubprotocol
```
