package handler

import (
	"net/http"

	"github.com/gorilla/websocket"

	"meshium/internal/mod/auth"
)

// upgradeWebSocket upgrades an HTTP request to a WebSocket connection, properly
// echoing back the subprotocol used for authentication. If the client requested
// a "meshium-auth.*" subprotocol, it is included in the response headers so the
// browser accepts the connection.
func upgradeWebSocket(upgrader websocket.Upgrader, w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	responseHeader := http.Header{}
	if proto := auth.WebSocketSubprotocolToken(r); proto != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", proto)
	}
	return upgrader.Upgrade(w, r, responseHeader)
}
