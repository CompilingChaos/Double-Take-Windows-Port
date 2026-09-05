package airplay

import (
	"errors"
	"testing"
	"time"
)

func TestSlowBroadcastSinkDoesNotBlockProducer(t *testing.T) {
	bc := &BroadcastCapture{}
	sink := bc.AddSink()
	defer sink.Close()

	done := make(chan error, 1)
	go func() {
		var err error
		for i := 0; i < cap(sink.queue)+2; i++ {
			err = sink.write([]byte{byte(i)})
			if err != nil {
				break
			}
		}
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, errSlowBroadcastSink) {
			t.Fatalf("slow sink write error = %v, want %v", err, errSlowBroadcastSink)
		}
	case <-time.After(time.Second):
		t.Fatal("producer blocked on slow broadcast sink")
	}
}
