package events

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/websocket"

	"github.com/rancher/go-machine-service/locks"
	"github.com/rancher/go-rancher/client"
)

const MaxWait = time.Duration(time.Second * 10)

// EventHandler Defines the function "interface" that handlers must conform to.
type EventHandler func(*Event, *client.RancherClient) error

type EventRouter struct {
	name          string
	priority      int
	apiURL        string
	accessKey     string
	secretKey     string
	apiClient     *client.RancherClient
	subscribeURL  string
	eventHandlers map[string]EventHandler
	workerCount   int
	eventStream   *websocket.Conn
	mu            *sync.Mutex
	resourceName  string
}

// The difference between Start and StartWithoutCreate is a matter of making this event router
// more generally usable. The current go-machine-service implementation creates
// the necessary ExternalHandler upon start up. This router has been refactor to
// be used in situations where creating an externalHandler is not desired.
// This allows the router to be used for Agent connections and for ExternalHandlers
// that are created outside of this router (we want to refactor gms to be that way).

func (router *EventRouter) Start(ready chan<- bool) error {
	err := router.createExternalHandler()
	if err != nil {
		return err
	}
	eventSuffix := ";handler=" + router.name
	return router.run(ready, eventSuffix)
}

func (router *EventRouter) StartWithoutCreate(ready chan<- bool) error {
	return router.run(ready, "")
}

func (router *EventRouter) createExternalHandler() error {
	// If it exists, delete it, then create it
	err := removeOldHandler(router.name, router.apiClient)
	if err != nil {
		return err
	}

	externalHandler := &client.ExternalHandler{
		Name:           router.name,
		Uuid:           router.name,
		Priority:       int64(router.priority),
		ProcessConfigs: make([]interface{}, len(router.eventHandlers)),
	}

	idx := 0
	for event := range router.eventHandlers {
		externalHandler.ProcessConfigs[idx] = ProcessConfig{
			Name:    event,
			OnError: router.resourceName + ".error",
		}
		idx++
	}
	err = createNewHandler(externalHandler, router.apiClient)
	if err != nil {
		return err
	}
	return nil
}

func (router *EventRouter) run(ready chan<- bool, eventSuffix string) (err error) {
	workers := make(chan *Worker, router.workerCount)
	for i := 0; i < router.workerCount; i++ {
		w := newWorker()
		workers <- w
	}

	log.WithFields(log.Fields{
		"workerCount": router.workerCount,
	}).Info("Initializing event router")

	handlers := map[string]EventHandler{}

	if pingHandler, ok := router.eventHandlers["ping"]; ok {
		// Ping doesnt need registered in the POST and ping events don't have the handler suffix.
		//If we start handling other non-suffix events, we might consider improving this.
		handlers["ping"] = pingHandler
	}

	subscribeParams := url.Values{}
	for event, handler := range router.eventHandlers {
		fullEventKey := event + eventSuffix
		subscribeParams.Add("eventNames", fullEventKey)
		handlers[fullEventKey] = handler
	}

	eventStream, err := subscribeToEvents(router.subscribeURL, router.accessKey, router.secretKey, subscribeParams)
	if err != nil {
		return err
	}
	log.Info("Connection established")
	router.eventStream = eventStream
	defer router.Stop()

	if ready != nil {
		ready <- true
	}

	for {
		_, message, err := eventStream.ReadMessage()
		if err != nil {
			// Error here means the connection is closed. It's normal, so just return.
			return nil
		}

		message = bytes.TrimSpace(message)
		if len(message) == 0 {
			continue
		}

		select {
		case worker := <-workers:
			go worker.DoWork(message, handlers, router.apiClient, workers)
		default:
			log.WithFields(log.Fields{
				"workerCount": router.workerCount,
			}).Info("No workers available dropping event.")
		}
	}
}

func (router *EventRouter) Stop() (err error) {
	if router.eventStream != nil {
		router.mu.Lock()
		defer router.mu.Unlock()
		if router.eventStream != nil {
			router.eventStream.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			router.eventStream = nil
		}
	}
	return nil
}

type Worker struct {
}

func (w *Worker) DoWork(rawEvent []byte, eventHandlers map[string]EventHandler, apiClient *client.RancherClient,
	workers chan *Worker) {
	defer func() { workers <- w }()

	event := &Event{}
	err := json.Unmarshal(rawEvent, &event)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Error("Error unmarshalling event")
		return
	}

	if event.Name != "ping" {
		log.WithFields(log.Fields{
			"event": string(rawEvent[:]),
		}).Debug("Processing event.")
	}

	unlocker := locks.Lock(event.ResourceID)
	if unlocker == nil {
		log.WithFields(log.Fields{
			"resourceId": event.ResourceID,
		}).Debug("Resource locked. Dropping event")
		return
	}
	defer unlocker.Unlock()

	if fn, ok := eventHandlers[event.Name]; ok {
		err = fn(event, apiClient)
		if err != nil {
			log.WithFields(log.Fields{
				"eventName":  event.Name,
				"eventId":    event.ID,
				"resourceId": event.ResourceID,
				"err":        err,
			}).Error("Error processing event")

			reply := &client.Publish{
				Name:                 event.ReplyTo,
				PreviousIds:          []string{event.ID},
				Transitioning:        "error",
				TransitioningMessage: err.Error(),
			}
			_, err := apiClient.Publish.Create(reply)
			if err != nil {
				log.WithFields(log.Fields{
					"err": err,
				}).Error("Error sending error-reply")
			}
		}
	} else {
		log.WithFields(log.Fields{
			"eventName": event.Name,
		}).Warn("No event handler registered for event")
	}
}

func NewEventRouter(name string, priority int, apiURL string, accessKey string, secretKey string,
	apiClient *client.RancherClient, eventHandlers map[string]EventHandler, resourceName string, workerCount int) (*EventRouter, error) {

	if apiClient == nil {
		var err error
		apiClient, err = client.NewRancherClient(&client.ClientOpts{
			Timeout:   time.Second * 30,
			Url:       apiURL,
			AccessKey: accessKey,
			SecretKey: secretKey,
		})
		if err != nil {
			return nil, err
		}
	}

	// TODO Get subscribe collection URL from API instead of hard coding
	subscribeURL := strings.Replace(apiURL+"/subscribe", "http", "ws", -1)

	return &EventRouter{
		name:          name,
		priority:      priority,
		apiURL:        apiURL,
		accessKey:     accessKey,
		secretKey:     secretKey,
		apiClient:     apiClient,
		subscribeURL:  subscribeURL,
		eventHandlers: eventHandlers,
		workerCount:   workerCount,
		mu:            &sync.Mutex{},
		resourceName:  resourceName,
	}, nil
}

func newWorker() *Worker {
	return &Worker{}
}

func subscribeToEvents(subscribeURL string, accessKey string, secretKey string, data url.Values) (*websocket.Conn, error) {
	dialer := &websocket.Dialer{}
	headers := http.Header{}
	headers.Add("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(accessKey+":"+secretKey)))
	subscribeURL = subscribeURL + "?" + data.Encode()
	ws, resp, err := dialer.Dial(subscribeURL, headers)
	if err != nil {
		defer resp.Body.Close()
		body, _ := ioutil.ReadAll(resp.Body)
		log.WithFields(log.Fields{
			"status":          resp.Status,
			"statusCode":      resp.StatusCode,
			"responseHeaders": resp.Header,
			"responseBody":    string(body[:]),
			"error":           err,
			"subscribeUrl":    subscribeURL,
		}).Error("Failed to subscribe to events.")
		return nil, err
	}
	return ws, nil
}

var createNewHandler = func(externalHandler *client.ExternalHandler, apiClient *client.RancherClient) error {
	_, err := apiClient.ExternalHandler.Create(externalHandler)
	return err
}

var removeOldHandler = func(name string, apiClient *client.RancherClient) error {
	listOpts := client.NewListOpts()
	listOpts.Filters["name"] = name
	listOpts.Filters["state"] = "active"
	handlers, err := apiClient.ExternalHandler.List(listOpts)
	if err != nil {
		return err
	}

	for _, handler := range handlers.Data {
		h := &handler
		log.WithFields(log.Fields{
			"handlerId": h.Id,
		}).Debug("Removing old handler")
		doneTransitioning := func() (bool, error) {
			handler, err := apiClient.ExternalHandler.ById(h.Id)
			if err != nil {
				return false, err
			}
			if handler == nil {
				return false, fmt.Errorf("Failed to lookup external handler %v.", handler.Id)
			}
			return handler.Transitioning != "yes", nil
		}

		if _, ok := h.Actions["deactivate"]; ok {
			h, err = apiClient.ExternalHandler.ActionDeactivate(h)
			if err != nil {
				return err
			}

			err = waitForTransition(doneTransitioning)
			if err != nil {
				return err
			}
		}

		h, err := apiClient.ExternalHandler.ById(h.Id)
		if err != nil {
			return err
		}
		if h != nil {
			if _, ok := h.Actions["remove"]; ok {
				h, err = apiClient.ExternalHandler.ActionRemove(h)
				if err != nil {
					return err
				}
				err = waitForTransition(doneTransitioning)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type ProcessConfig struct {
	Name    string `json:"name"`
	OnError string `json:"onError"`
}

type doneTranitioningFunc func() (bool, error)

func waitForTransition(waitFunc doneTranitioningFunc) error {
	timeoutAt := time.Now().Add(MaxWait)
	ticker := time.NewTicker(time.Millisecond * 250)
	defer ticker.Stop()
	for tick := range ticker.C {
		done, err := waitFunc()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if tick.After(timeoutAt) {
			return fmt.Errorf("Timed out waiting for transtion.")
		}
	}
	return fmt.Errorf("Timed out waiting for transtion.")
}
