package mcp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestServeCancelsWaitAndAnswersFollowingRequest(t *testing.T) {
	in, send := io.Pipe()
	out, receive := io.Pipe()
	defer func() { _ = in.Close(); _ = send.Close(); _ = out.Close(); _ = receive.Close() }()
	started, cancelled := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- serve(in, receive, func(ctx context.Context, req request) (response, bool) {
			if req.Method == "slow" {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return response{JSONRPC: "2.0", ID: req.ID, Result: "late"}, true
			}
			return dispatch(ctx, req, true)
		})
	}()
	enc := json.NewEncoder(send)
	if err := enc.Encode(request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "slow"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request never started")
	}
	if err := enc.Encode(request{JSONRPC: "2.0", Method: "notifications/cancelled", Params: json.RawMessage(`{"requestId":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(request{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"}); err != nil {
		t.Fatal(err)
	}
	result := make(chan response, 1)
	go func() { var r response; _ = json.NewDecoder(out).Decode(&r); result <- r }()
	select {
	case r := <-result:
		if string(r.ID) != "2" {
			t.Fatalf("unexpected response: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("follow-up blocked behind cancelled wait")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("handler was not cancelled")
	}
	_ = send.Close()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServeCancelsPendingRequestOnDisconnect(t *testing.T) {
	in, send := io.Pipe()
	defer func() { _ = in.Close(); _ = send.Close() }()
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- serve(in, io.Discard, func(ctx context.Context, req request) (response, bool) {
			close(started)
			<-ctx.Done()
			return response{}, false
		})
	}()
	if _, err := io.WriteString(send, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"slow\"}\n"); err != nil {
		t.Fatal(err)
	}
	<-started
	_ = send.Close()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect left a waiter running")
	}
}
