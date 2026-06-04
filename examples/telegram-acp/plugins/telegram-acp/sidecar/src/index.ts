import http from "node:http";

// Minimal placeholder showing the sidecar pattern.
// In a real implementation, this would be the channel-manager code
// that connects to the agent via ACP and bridges to Telegram.

const AGENT_ACP_URL = process.env.AGENT_ACP_URL || "http://gateway:8080/agent";
const PORT = 3000;

const server = http.createServer((req, res) => {
  if (req.url === "/health") {
    res.writeHead(200);
    res.end("ok");
    return;
  }
  res.writeHead(404);
  res.end();
});

server.listen(PORT, () => {
  console.log(`Telegram ACP sidecar running on :${PORT}`);
  console.log(`Agent ACP URL: ${AGENT_ACP_URL}`);
});
