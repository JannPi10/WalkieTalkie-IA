package httphandler

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"walkie-backend/internal/httpHandler/handlers"
)

func TestRoutes_RegistersHandlers(t *testing.T) {
	mux := http.NewServeMux()
	Routes(mux)

	tests := []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/channels/public", handlers.ListPublicChannels},
		{"/channel-users", handlers.ChannelUsers},
		{"/ws", handlers.HandleWebSocket},
		{"/audio/ingest", handlers.AudioIngest},
		{"/audio/poll", handlers.AudioPoll},
		{"/auth", handlers.Authenticate},
	}

	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		gotHandler, pattern := mux.Handler(req)

		if pattern != tc.path {
			t.Fatalf("path %s: expected pattern %s, got %s", tc.path, tc.path, pattern)
		}

		hf, ok := gotHandler.(http.HandlerFunc)
		if !ok {
			t.Fatalf("path %s: handler is %T, expected httpHandler.HandlerFunc", tc.path, gotHandler)
		}

		if reflect.ValueOf(hf).Pointer() != reflect.ValueOf(tc.handler).Pointer() {
			t.Fatalf("path %s: unexpected handler registration", tc.path)
		}
	}
}
