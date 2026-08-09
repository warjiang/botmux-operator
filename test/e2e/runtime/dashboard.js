import http from "node:http";
import crypto from "node:crypto";

const server = http.createServer((req, res) => {
  if (req.url === "/__health") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end('{"ok":true}');
    return;
  }
  res.writeHead(200, { "content-type": "text/plain" });
  res.end("botmux e2e dashboard");
});

server.on("upgrade", (req, socket) => {
  if (req.url?.startsWith("/s/")) {
    const key = req.headers["sec-websocket-key"];
    if (!key) {
      socket.destroy();
      return;
    }
    const accept = crypto
      .createHash("sha1")
      .update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
      .digest("base64");
    socket.write(
      "HTTP/1.1 101 Switching Protocols\r\n" +
      "Upgrade: websocket\r\n" +
      "Connection: Upgrade\r\n" +
      `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
    socket.end();
    return;
  }
  socket.destroy();
});

server.listen(7891, "0.0.0.0");
const shutdown = () => server.close(() => process.exit(0));
process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);
