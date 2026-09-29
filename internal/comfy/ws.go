package comfy

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// Binary websocket events (ComfyUI's BinaryEventTypes).
const (
	eventImage             = 1 // [format: 1 JPEG, 2 PNG][image]
	eventImageWithMetadata = 4 // [metadata length][metadata JSON][image]
)

// message is a websocket message: JSON (typ from its "type") or an image.
type message struct {
	typ  string
	data json.RawMessage
	// Binary messages: typ "image" (an image from a node: the output of a
	// websocket node, or a sampler preview on servers that do not label
	// previews) or "preview" (a labelled sampler preview).
	img      []byte
	format   string
	promptID string
}

// dial opens the websocket for clientID and asks for labelled previews,
// which keeps sampler previews apart from output images.
func (c *Client) dial(ctx context.Context, clientID string) (*websocket.Conn, error) {
	u := c.endpoint
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	default:
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	ws, _, err := websocket.Dial(ctx, u+"/ws?clientId="+url.QueryEscape(clientID), nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(1 << 30)
	flags := `{"type":"feature_flags","data":{"supports_preview_metadata":true}}`
	if err := ws.Write(ctx, websocket.MessageText, []byte(flags)); err != nil {
		ws.CloseNow()
		return nil, err
	}
	return ws, nil
}

func parseMessage(typ websocket.MessageType, data []byte) (message, bool) {
	if typ == websocket.MessageText {
		var m struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type == "" {
			return message{}, false
		}
		return message{typ: m.Type, data: m.Data}, true
	}
	if len(data) < 8 {
		return message{}, false
	}
	switch binary.BigEndian.Uint32(data[:4]) {
	case eventImage:
		format := "jpeg"
		if binary.BigEndian.Uint32(data[4:8]) == 2 {
			format = "png"
		}
		return message{typ: "image", img: data[8:], format: format}, true
	case eventImageWithMetadata:
		n := int(binary.BigEndian.Uint32(data[4:8]))
		if 8+n > len(data) {
			return message{}, false
		}
		var meta struct {
			ImageType string `json:"image_type"`
			PromptID  string `json:"prompt_id"`
		}
		_ = json.Unmarshal(data[8:8+n], &meta)
		format := "jpeg"
		if strings.Contains(meta.ImageType, "png") {
			format = "png"
		}
		return message{typ: "preview", img: data[8+n:], format: format, promptID: meta.PromptID}, true
	}
	return message{}, false
}

func readLoop(ctx context.Context, ws *websocket.Conn, out chan<- message, errc chan<- error) {
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			errc <- err
			return
		}
		m, ok := parseMessage(typ, data)
		if !ok {
			continue
		}
		select {
		case out <- m:
		case <-ctx.Done():
			return
		}
	}
}
