package redfish

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/tjst-t/qemu-bmc/internal/events"
)

const (
	eventServicePath    = "/redfish/v1/EventService"
	subscriptionsPath   = eventServicePath + "/Subscriptions"
	submitTestEventPath = eventServicePath + "/Actions/EventService.SubmitTestEvent"

	// Advertised on the EventService and applied by the dispatcher.
	deliveryRetryAttempts        = 3
	deliveryRetryIntervalSeconds = 5

	eventFormatEvent        = "Event"
	eventFormatMetricReport = "MetricReport"
)

var (
	eventFormatTypes      = []string{eventFormatEvent, eventFormatMetricReport}
	deliveryRetryPolicies = []string{"TerminateAfterRetries", "SuspendRetries", "RetryForever", "RetryForeverWithBackoff"}
	deprecatedEventTypes  = []string{"StatusChange", "ResourceUpdated", "ResourceAdded", "ResourceRemoved", "Alert", "MetricReport", "Other"}
)

func subscriptionPath(id string) string {
	return subscriptionsPath + "/" + id
}

func (s *Server) handleGetEventService(w http.ResponseWriter, r *http.Request) {
	svc := EventService{
		ODataType:                    "#EventService.v1_7_0.EventService",
		ODataID:                      eventServicePath,
		ID:                           "EventService",
		Name:                         "Event Service",
		ServiceEnabled:               true,
		DeliveryRetryAttempts:        deliveryRetryAttempts,
		DeliveryRetryIntervalSeconds: deliveryRetryIntervalSeconds,
		EventFormatTypes:             eventFormatTypes,
		Subscriptions:                ODataID{ODataID: subscriptionsPath},
		Actions: EventServiceActions{
			SubmitTestEvent: ActionTarget{Target: submitTestEventPath},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(svc)
}

// listSubscriptions returns all subscriptions ordered by numeric Id.
func (s *Server) listSubscriptions() []EventDestination {
	s.mu.RLock()
	defer s.mu.RUnlock()
	subs := make([]EventDestination, 0, len(s.subscriptions))
	for _, sub := range s.subscriptions {
		subs = append(subs, sub)
	}
	sort.Slice(subs, func(i, j int) bool {
		a, _ := strconv.Atoi(subs[i].ID)
		b, _ := strconv.Atoi(subs[j].ID)
		return a < b
	})
	return subs
}

func (s *Server) handleSubscriptionCollection(w http.ResponseWriter, r *http.Request) {
	subs := s.listSubscriptions()
	members := make([]ODataID, 0, len(subs))
	for _, sub := range subs {
		members = append(members, ODataID{ODataID: sub.ODataID})
	}
	col := EventDestinationCollection{
		ODataType:    "#EventDestinationCollection.EventDestinationCollection",
		ODataID:      subscriptionsPath,
		Name:         "Event Subscriptions Collection",
		MembersCount: len(members),
		Members:      members,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

// validateSubscription checks the create request and fills in defaults.
// It returns a Redfish message id and text on failure.
func validateSubscription(req *CreateEventDestinationRequest) (string, string) {
	if req.Destination == "" {
		return "PropertyMissing", "Destination is required"
	}
	u, err := url.Parse(req.Destination)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "PropertyValueFormatError", "Destination must be an absolute http or https URI"
	}
	if req.Protocol == "" {
		req.Protocol = "Redfish"
	}
	if req.Protocol != "Redfish" {
		return "PropertyValueNotInList", "Protocol must be Redfish"
	}
	if req.SubscriptionType == "" {
		req.SubscriptionType = "RedfishEvent"
	}
	if req.SubscriptionType != "RedfishEvent" {
		return "PropertyValueNotInList", "SubscriptionType must be RedfishEvent"
	}
	if req.EventFormatType == "" {
		req.EventFormatType = eventFormatEvent
	}
	if !slices.Contains(eventFormatTypes, req.EventFormatType) {
		return "PropertyValueNotInList", "EventFormatType must be one of Event, MetricReport"
	}
	if req.DeliveryRetryPolicy != "" && !slices.Contains(deliveryRetryPolicies, req.DeliveryRetryPolicy) {
		return "PropertyValueNotInList", "DeliveryRetryPolicy must be one of TerminateAfterRetries, SuspendRetries, RetryForever, RetryForeverWithBackoff"
	}
	for _, t := range req.EventTypes {
		if !slices.Contains(deprecatedEventTypes, t) {
			return "PropertyValueNotInList", "EventTypes contains unsupported value " + strconv.Quote(t)
		}
	}
	return "", ""
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req CreateEventDestinationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}
	if code, msg := validateSubscription(&req); code != "" {
		writeError(w, http.StatusBadRequest, code, msg)
		return
	}

	s.mu.Lock()
	for _, sub := range s.subscriptions {
		if sub.Destination == req.Destination && sub.EventFormatType == req.EventFormatType {
			s.mu.Unlock()
			writeError(w, http.StatusConflict, "ResourceAlreadyExists",
				"a subscription for "+req.Destination+" with EventFormatType "+req.EventFormatType+" already exists")
			return
		}
	}
	s.nextSubscriptionID++
	id := strconv.Itoa(s.nextSubscriptionID)
	sub := EventDestination{
		ODataType:           "#EventDestination.v1_9_0.EventDestination",
		ODataID:             subscriptionPath(id),
		ID:                  id,
		Name:                "Event Subscription " + id,
		Destination:         req.Destination,
		Context:             req.Context,
		Protocol:            req.Protocol,
		SubscriptionType:    req.SubscriptionType,
		EventFormatType:     req.EventFormatType,
		DeliveryRetryPolicy: req.DeliveryRetryPolicy,
		EventTypes:          req.EventTypes,
		RegistryPrefixes:    req.RegistryPrefixes,
		ResourceTypes:       req.ResourceTypes,
	}
	s.subscriptions[id] = sub
	s.mu.Unlock()
	s.debugf("EventService: created subscription %s (destination=%s, format=%s, context=%q)",
		id, sub.Destination, sub.EventFormatType, sub.Context)

	w.Header().Set("Location", sub.ODataID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(sub)
}

func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	sub, ok := s.subscriptions[mux.Vars(r)["id"]]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sub)
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	s.mu.Lock()
	_, ok := s.subscriptions[id]
	delete(s.subscriptions, id)
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found")
		return
	}
	s.debugf("EventService: deleted subscription %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// parseOriginOfCondition accepts both the plain URI string and the
// {"@odata.id": ...} link form.
func parseOriginOfCondition(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var uri string
	if err := json.Unmarshal(raw, &uri); err == nil {
		return uri, true
	}
	var link ODataID
	if err := json.Unmarshal(raw, &link); err == nil {
		return link.ODataID, true
	}
	return "", false
}

func (s *Server) handleSubmitTestEvent(w http.ResponseWriter, r *http.Request) {
	var req SubmitTestEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}
	if req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "ActionParameterMissing", "MessageId is required")
		return
	}
	origin, ok := parseOriginOfCondition(req.OriginOfCondition)
	if !ok {
		writeError(w, http.StatusBadRequest, "ActionParameterValueTypeError",
			"OriginOfCondition must be a URI string or an @odata.id link")
		return
	}

	s.mu.Lock()
	s.nextEventID++
	payloadID := strconv.Itoa(s.nextEventID)
	s.mu.Unlock()

	ev := events.Event{
		MemberID:        "0",
		EventID:         req.EventID,
		EventTimestamp:  req.EventTimestamp,
		Severity:        req.Severity,
		MessageSeverity: req.MessageSeverity,
		Message:         req.Message,
		MessageID:       req.MessageID,
		MessageArgs:     req.MessageArgs,
	}
	if ev.EventID == "" {
		ev.EventID = payloadID
	}
	if ev.EventTimestamp == "" {
		ev.EventTimestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if ev.MessageArgs == nil {
		ev.MessageArgs = []string{}
	}
	if origin != "" {
		ev.OriginOfCondition = &events.ODataID{ODataID: origin}
	}

	sent := 0
	for _, sub := range s.listSubscriptions() {
		if sub.EventFormatType != eventFormatEvent {
			continue
		}
		s.eventDispatcher.Send(sub.Destination, events.NewPayload(payloadID, sub.Context, []events.Event{ev}))
		sent++
	}
	s.debugf("EventService: submitted test event %s (MessageId=%s) to %d subscription(s)", payloadID, req.MessageID, sent)
	w.WriteHeader(http.StatusNoContent)
}
