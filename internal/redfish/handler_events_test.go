package redfish

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

// metalOperatorSubscription mirrors the body metal-operator's
// CreateEventSubscription POSTs.
const metalOperatorSubscription = `{
	"Destination": "http://172.16.100.2:30092/serverevents/metal-maintenance-operator/alerts/node1",
	"EventTypes": [],
	"EventFormatType": "Event",
	"DeliveryRetryPolicy": "RetryForever",
	"Protocol": "Redfish",
	"Context": "metal-operator"
}`

func newEventServer() *Server {
	return NewServer(newMockMachine(qmp.StatusRunning), "", "", "")
}

func TestServiceRoot_LinksEventService(t *testing.T) {
	w := doAccountRequest(newEventServer(), "GET", "/redfish/v1", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var root ServiceRoot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &root))
	assert.Equal(t, "/redfish/v1/EventService", root.EventService.ODataID)
}

func TestGetEventService(t *testing.T) {
	w := doAccountRequest(newEventServer(), "GET", "/redfish/v1/EventService", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var svc EventService
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &svc))
	assert.Equal(t, "#EventService.v1_7_0.EventService", svc.ODataType)
	assert.True(t, svc.ServiceEnabled)
	assert.Equal(t, []string{"Event", "MetricReport"}, svc.EventFormatTypes)
	assert.Equal(t, "/redfish/v1/EventService/Subscriptions", svc.Subscriptions.ODataID)
	assert.Equal(t, "/redfish/v1/EventService/Actions/EventService.SubmitTestEvent", svc.Actions.SubmitTestEvent.Target)
}

func TestCreateSubscription(t *testing.T) {
	srv := newEventServer()

	w := doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions", metalOperatorSubscription, "", "")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, "/redfish/v1/EventService/Subscriptions/1", w.Header().Get("Location"))

	w = doAccountRequest(srv, "GET", "/redfish/v1/EventService/Subscriptions", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var col EventDestinationCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &col))
	require.Equal(t, 1, col.MembersCount)
	assert.Equal(t, "/redfish/v1/EventService/Subscriptions/1", col.Members[0].ODataID)

	w = doAccountRequest(srv, "GET", "/redfish/v1/EventService/Subscriptions/1", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var sub EventDestination
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sub))
	assert.Equal(t, "http://172.16.100.2:30092/serverevents/metal-maintenance-operator/alerts/node1", sub.Destination)
	assert.Equal(t, "metal-operator", sub.Context)
	assert.Equal(t, "Event", sub.EventFormatType)
	assert.Equal(t, "Redfish", sub.Protocol)
	assert.Equal(t, "RedfishEvent", sub.SubscriptionType)
	assert.Equal(t, "RetryForever", sub.DeliveryRetryPolicy)
}

func TestCreateSubscription_DefaultsAndMetricReport(t *testing.T) {
	srv := newEventServer()

	w := doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions",
		`{"Destination": "https://collector/metricsreport/node1", "EventFormatType": "MetricReport"}`, "", "")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var sub EventDestination
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sub))
	assert.Equal(t, "MetricReport", sub.EventFormatType)
	assert.Equal(t, "Redfish", sub.Protocol)

	w = doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions",
		`{"Destination": "https://collector/alerts/node1"}`, "", "")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sub))
	assert.Equal(t, "Event", sub.EventFormatType)
}

func TestCreateSubscription_Validation(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
	}{
		{"malformed", `{`, "MalformedJSON"},
		{"missing destination", `{"Protocol": "Redfish"}`, "PropertyMissing"},
		{"relative destination", `{"Destination": "/events"}`, "PropertyValueFormatError"},
		{"unsupported scheme", `{"Destination": "ftp://collector/events"}`, "PropertyValueFormatError"},
		{"protocol", `{"Destination": "http://collector", "Protocol": "SNMPv2c"}`, "PropertyValueNotInList"},
		{"format", `{"Destination": "http://collector", "EventFormatType": "Syslog"}`, "PropertyValueNotInList"},
		{"retry policy", `{"Destination": "http://collector", "DeliveryRetryPolicy": "Sometimes"}`, "PropertyValueNotInList"},
		{"event types", `{"Destination": "http://collector", "EventTypes": ["Everything"]}`, "PropertyValueNotInList"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doAccountRequest(newEventServer(), "POST", "/redfish/v1/EventService/Subscriptions", tt.body, "", "")
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), tt.code)
		})
	}
}

func TestCreateSubscription_Duplicate(t *testing.T) {
	srv := newEventServer()

	w := doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions", metalOperatorSubscription, "", "")
	require.Equal(t, http.StatusCreated, w.Code)

	w = doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions", metalOperatorSubscription, "", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "ResourceAlreadyExists")
}

func TestDeleteSubscription(t *testing.T) {
	srv := newEventServer()

	w := doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions", metalOperatorSubscription, "", "")
	require.Equal(t, http.StatusCreated, w.Code)

	w = doAccountRequest(srv, "DELETE", "/redfish/v1/EventService/Subscriptions/1", "", "", "")
	assert.Equal(t, http.StatusNoContent, w.Code)

	w = doAccountRequest(srv, "GET", "/redfish/v1/EventService/Subscriptions/1", "", "", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = doAccountRequest(srv, "DELETE", "/redfish/v1/EventService/Subscriptions/1", "", "", "")
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Ids are not reused after a delete.
	w = doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions", metalOperatorSubscription, "", "")
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "/redfish/v1/EventService/Subscriptions/2", w.Header().Get("Location"))
}

func TestSubmitTestEvent_DeliversToEventSubscriptions(t *testing.T) {
	received := make(chan map[string]any, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		payload["path"] = r.URL.Path
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	srv := newEventServer()
	w := doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions",
		`{"Destination": "`+receiver.URL+`/alerts/node1", "EventFormatType": "Event", "Context": "metal-operator"}`, "", "")
	require.Equal(t, http.StatusCreated, w.Code)
	w = doAccountRequest(srv, "POST", "/redfish/v1/EventService/Subscriptions",
		`{"Destination": "`+receiver.URL+`/metricsreport/node1", "EventFormatType": "MetricReport"}`, "", "")
	require.Equal(t, http.StatusCreated, w.Code)

	// Body as sent by metal-maintenance-operator's health check (HPE form).
	w = doAccountRequest(srv, "POST", "/redfish/v1/EventService/Actions/EventService.SubmitTestEvent", `{
		"EventId": "1",
		"EventTimestamp": "2026-10-12T10:00:00Z",
		"MessageId": "TestEventId",
		"Message": "metal-maintenance-operator pipeline health check",
		"MessageArgs": [],
		"Severity": "OK",
		"OriginOfCondition": "/redfish/v1/Managers/1"
	}`, "", "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	var payload map[string]any
	select {
	case payload = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("test event was not delivered")
	}
	assert.Equal(t, "/alerts/node1", payload["path"])
	assert.Equal(t, "#Event.v1_7_0.Event", payload["@odata.type"])
	assert.Equal(t, "metal-operator", payload["Context"])
	evs := payload["Events"].([]any)
	require.Len(t, evs, 1)
	ev := evs[0].(map[string]any)
	assert.Equal(t, "TestEventId", ev["MessageId"])
	assert.Equal(t, "OK", ev["Severity"])
	assert.Equal(t, "1", ev["EventId"])
	assert.Equal(t, map[string]any{"@odata.id": "/redfish/v1/Managers/1"}, ev["OriginOfCondition"])

	// MetricReport subscriptions don't receive alerts.
	select {
	case extra := <-received:
		t.Fatalf("unexpected delivery to %v", extra["path"])
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSubmitTestEvent_Validation(t *testing.T) {
	srv := newEventServer()

	w := doAccountRequest(srv, "POST", "/redfish/v1/EventService/Actions/EventService.SubmitTestEvent", `{"Severity": "OK"}`, "", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "ActionParameterMissing")

	w = doAccountRequest(srv, "POST", "/redfish/v1/EventService/Actions/EventService.SubmitTestEvent",
		`{"MessageId": "TestEventId", "OriginOfCondition": 42}`, "", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = doAccountRequest(srv, "POST", "/redfish/v1/EventService/Actions/EventService.SubmitTestEvent",
		`{"MessageId": "TestEventId", "OriginOfCondition": {"@odata.id": "/redfish/v1/Systems/1"}}`, "", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestEventService_RequiresAuth(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")

	w := doAccountRequest(srv, "GET", "/redfish/v1/EventService/Subscriptions", "", "", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = doAccountRequest(srv, "GET", "/redfish/v1/EventService/Subscriptions", "", "admin", "password")
	assert.Equal(t, http.StatusOK, w.Code)
}
