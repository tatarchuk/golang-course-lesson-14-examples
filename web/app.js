// Shared script of the example pages: the demo login, then one WebSocket (chat, dashboard)
// or one EventSource (SSE dashboard) per page. Everything the server sends is shown with
// textContent, never parsed as HTML.
(function () {
  "use strict";

  var page = document.body.dataset.page;
  var token = sessionStorage.getItem("token");
  var user = sessionStorage.getItem("user");

  function byId(id) { return document.getElementById(id); }
  function setStatus(text) { byId("status").textContent = text; }
  function addLine(text) {
    var li = document.createElement("li");
    li.textContent = new Date().toLocaleTimeString() + "  " + text;
    byId("log").appendChild(li);
    li.scrollIntoView({ block: "end" });
  }
  function wsURL(path) {
    var scheme = location.protocol === "https:" ? "wss://" : "ws://";
    var sep = path.indexOf("?") < 0 ? "?" : "&";
    return scheme + location.host + path + sep + "token=" + encodeURIComponent(token);
  }
  function closed(e) {
    setStatus("closed (" + e.code + (e.reason ? " " + e.reason : "") + ")");
  }

  if (user) { byId("who").textContent = "as " + user; }

  if (page === "index") {
    if (token) { byId("pages").hidden = false; }
    byId("login").addEventListener("submit", function (e) {
      e.preventDefault();
      var name = new FormData(e.target).get("username");
      fetch("/api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: name })
      }).then(function (r) {
        if (!r.ok) { throw new Error("login failed: " + r.status); }
        return r.json();
      }).then(function (d) {
        sessionStorage.setItem("token", d.token);
        sessionStorage.setItem("user", d.user);
        token = d.token;
        byId("who").textContent = "as " + d.user;
        byId("pages").hidden = false;
        setStatus("");
      }).catch(function (err) { setStatus(err.message); });
    });
    return;
  }

  if (!token) { location.href = "index.html"; return; }

  if (page === "chat") {
    var room = new URLSearchParams(location.search).get("room") || "general";
    byId("room").textContent = room;
    var ws = new WebSocket(wsURL("/ws/chat?room=" + encodeURIComponent(room)));
    ws.onopen = function () { setStatus("connected"); };
    ws.onclose = closed;
    ws.onmessage = function (e) {
      var m = JSON.parse(e.data);
      addLine(m.author + ": " + m.text);
    };
    byId("send").addEventListener("submit", function (e) {
      e.preventDefault();
      var input = e.target.elements.text;
      ws.send(JSON.stringify({ room: room, text: input.value }));
      input.value = "";
    });
  } else if (page === "dashboard") {
    var dash = new WebSocket(wsURL("/ws/dashboard"));
    dash.onopen = function () { setStatus("connected"); };
    dash.onclose = closed;
    dash.onmessage = function (e) { addLine(e.data); };
  } else if (page === "sse") {
    var es = new EventSource("/sse/dashboard?token=" + encodeURIComponent(token));
    es.onopen = function () { setStatus("connected"); };
    es.onerror = function () { setStatus("reconnecting (EventSource retries on its own)"); };
    es.onmessage = function (e) { addLine(e.data); };
  }
})();
