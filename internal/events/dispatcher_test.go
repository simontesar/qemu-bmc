package events

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPayload() Payload {
	return NewPayload("1", "metal-operator", []Event{{
		MemberID:          "0",
		EventID:           "42",
		EventTimestamp:    "2026-10-12T10:00:00Z",
		Severity:          "OK",
		Message:           "test",
		MessageID:         "TestEventId",
		MessageArgs:       []string{},
		OriginOfCondition: &ODataID{ODataID: "/redfish/v1/Managers/1"},
	}})
}

func TestDeliver_PostsEventPayload(t *testing.T) {
	var got map[string]any
	var contentType string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	d := NewDispatcher(1, 0, nil)
	require.NoError(t, d.Deliver(context.Background(), receiver.URL+"/serverevents/alerts/bmc", testPayload()))

	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, "#Event.v1_7_0.Event", got["@odata.type"])
	assert.Equal(t, "metal-operator", got["Context"])
	evs := got["Events"].([]any)
	require.Len(t, evs, 1)
	ev := evs[0].(map[string]any)
	assert.Equal(t, "TestEventId", ev["MessageId"])
	assert.Equal(t, "OK", ev["Severity"])
	assert.Equal(t, "42", ev["EventId"])
	assert.Equal(t, map[string]any{"@odata.id": "/redfish/v1/Managers/1"}, ev["OriginOfCondition"])
}

func TestDeliver_RetriesUntilSuccess(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	d := NewDispatcher(3, time.Millisecond, nil)
	require.NoError(t, d.Deliver(context.Background(), receiver.URL, testPayload()))
	assert.Equal(t, int32(3), calls.Load())
}

func TestDeliver_GivesUpAfterAttempts(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer receiver.Close()

	d := NewDispatcher(2, time.Millisecond, nil)
	err := d.Deliver(context.Background(), receiver.URL, testPayload())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after 2 attempts")
	assert.Equal(t, int32(2), calls.Load())
}

func TestDeliver_AcceptsSelfSignedHTTPS(t *testing.T) {
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	d := NewDispatcher(1, 0, nil)
	require.NoError(t, d.Deliver(context.Background(), receiver.URL, testPayload()))
}
