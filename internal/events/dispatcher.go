// Package events delivers Redfish event payloads to subscription destinations.
package events

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// ODataID is a Redfish resource link.
type ODataID struct {
	ODataID string `json:"@odata.id"`
}

// Event is one record of a Redfish Event payload.
type Event struct {
	MemberID          string   `json:"MemberId"`
	EventID           string   `json:"EventId"`
	EventTimestamp    string   `json:"EventTimestamp"`
	Severity          string   `json:"Severity,omitempty"`
	MessageSeverity   string   `json:"MessageSeverity,omitempty"`
	Message           string   `json:"Message,omitempty"`
	MessageID         string   `json:"MessageId"`
	MessageArgs       []string `json:"MessageArgs"`
	OriginOfCondition *ODataID `json:"OriginOfCondition,omitempty"`
}

// Payload is the body POSTed to a subscription destination
// (#Event.v1_7_0.Event).
type Payload struct {
	ODataType string  `json:"@odata.type"`
	ID        string  `json:"Id"`
	Name      string  `json:"Name"`
	Context   string  `json:"Context,omitempty"`
	Events    []Event `json:"Events"`
}

// NewPayload wraps events into an Event payload for one subscription.
func NewPayload(id, context string, evs []Event) Payload {
	return Payload{
		ODataType: "#Event.v1_7_0.Event",
		ID:        id,
		Name:      "Event Array",
		Context:   context,
		Events:    evs,
	}
}

// Dispatcher POSTs event payloads to subscription destinations, retrying
// failed deliveries a fixed number of times.
type Dispatcher struct {
	client        *http.Client
	attempts      int
	retryInterval time.Duration
	debugf        func(format string, args ...any)
}

// NewDispatcher creates a Dispatcher. attempts is the total number of delivery
// attempts per payload (at least 1). Destinations served over https are not
// verified, as a BMC usually can't validate an in-cluster receiver's certificate.
func NewDispatcher(attempts int, retryInterval time.Duration, debugf func(format string, args ...any)) *Dispatcher {
	if attempts < 1 {
		attempts = 1
	}
	if debugf == nil {
		debugf = func(string, ...any) {}
	}
	return &Dispatcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see NewDispatcher
			},
		},
		attempts:      attempts,
		retryInterval: retryInterval,
		debugf:        debugf,
	}
}

// Send delivers payload to destination in the background.
func (d *Dispatcher) Send(destination string, payload Payload) {
	go func() {
		if err := d.Deliver(context.Background(), destination, payload); err != nil {
			log.Printf("EventService: delivery to %s failed: %v", destination, err)
		}
	}()
}

// Deliver POSTs payload to destination, retrying until a 2xx response or the
// attempts are exhausted.
func (d *Dispatcher) Deliver(ctx context.Context, destination string, payload Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	var lastErr error
	for attempt := 1; attempt <= d.attempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d.retryInterval):
			}
		}
		if lastErr = d.post(ctx, destination, body); lastErr == nil {
			d.debugf("EventService: delivered event %s to %s (attempt %d)", payload.ID, destination, attempt)
			return nil
		}
		d.debugf("EventService: delivery attempt %d/%d to %s failed: %v", attempt, d.attempts, destination, lastErr)
	}
	return fmt.Errorf("after %d attempts: %w", d.attempts, lastErr)
}

func (d *Dispatcher) post(ctx context.Context, destination string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}
