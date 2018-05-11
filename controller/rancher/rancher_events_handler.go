package rancher

import (
	"fmt"
	"sync"
	"time"

	"github.com/leodotcloud/log"
	revents "github.com/rancher/event-subscriber/events"
	"github.com/rancher/go-rancher/v2"
	"github.com/rancher/lb-controller/config"
)

type EventsHandler interface {
	Subscribe() error
}

type REventsHandler struct {
	CattleURL       string
	CattleAccessKey string
	CattleSecretKey string
	DoOnEvent       func(*config.Endpoint) bool
	CheckOnEvent    func(*config.Endpoint) bool
	DoOnTimeout     func(*config.Endpoint)
	PollStatus      func(*config.Endpoint) bool
	EventMap        map[string]*revents.Event
	EventMu         *sync.RWMutex
}

func (ehandler *REventsHandler) Subscribe() error {

	eventHandlers := map[string]revents.EventHandler{
		"target.drain": ehandler.handle,
		"ping":         ehandler.handle,
	}

	router, err := revents.NewEventRouter("", 0, ehandler.CattleURL, ehandler.CattleAccessKey, ehandler.CattleSecretKey, nil, eventHandlers, "", 250, revents.DefaultPingConfig)
	if err != nil {
		return err
	}

	go func() {
		sp := revents.SkippingWorkerPool(250, nil)
		for {
			if err := router.RunWithWorkerPool(sp); err != nil {
				log.Debugf("Exiting subscriber: %v", err)
			}
			time.Sleep(time.Second)
		}
	}()

	return nil
}

func (ehandler *REventsHandler) handle(event *revents.Event, cli *client.RancherClient) error {
	log.Debugf("Rancher event: %#v eventName=%v eventID=%v resourceID=%v", event, event.Name, event.ID, event.ResourceID)

	if event.Name == "target.drain" {
		sendReply, err := ehandler.HandleDrainEvent(event, cli)
		if err != nil {
			log.Errorf("Error handling drain event: %#v eventName=%v eventID=%v resourceID=%v", err, event.Name, event.ID, event.ResourceID)
			return err
		}
		if !sendReply {
			return nil
		}
		if err := ehandler.CreateAndPublishReply(event, cli); err != nil {
			log.Errorf("Error replying to the event: %#v eventName=%v eventID=%v resourceID=%v", err, event.Name, event.ID, event.ResourceID)
			return err
		}
	}
	return nil
}

func (ehandler *REventsHandler) NewReply(event *revents.Event) *client.Publish {
	return &client.Publish{
		Name:         event.ReplyTo,
		PreviousIds:  []string{event.ID},
		ResourceType: event.ResourceType,
		ResourceId:   event.ResourceID,
	}
}

func (ehandler *REventsHandler) PublishReply(reply *client.Publish, apiClient *client.RancherClient) error {
	_, err := apiClient.Publish.Create(reply)
	return err
}

func (ehandler *REventsHandler) CreateAndPublishReply(event *revents.Event, cli *client.RancherClient) error {
	reply := ehandler.NewReply(event)
	log.Infof("New reply: %#v created to the event: %#v", reply, event)

	if reply.Name == "" {
		return nil
	}
	err := ehandler.PublishReply(reply, cli)
	if err != nil {
		return err
	}
	return nil
}

func (ehandler *REventsHandler) ErrorReply(event *revents.Event, cli *client.RancherClient, eventError error) error {
	reply := ehandler.NewReply(event)
	if reply.Name == "" {
		return nil
	}
	reply.Transitioning = "error"
	reply.TransitioningMessage = eventError.Error()
	err := ehandler.PublishReply(reply, cli)
	if err != nil {
		return err
	}
	return nil
}

func (ehandler *REventsHandler) HandleDrainEvent(event *revents.Event, cli *client.RancherClient) (bool, error) {

	//form the endpoint from the eventVO

	log.Infof("Received target.drain IP: %v, drainTimeout: %v, eventID: %v, resourceID %v", event.Data["targetIPaddress"], event.Data["drainTimeout"], event.ID, event.ResourceID)

	primaryIP, ok := event.Data["targetIPaddress"]

	if ok {
		if primaryIP == nil {
			//just reply to this event, but no drain needed
			return true, nil
		}
		ep := &config.Endpoint{
			IP:           primaryIP.(string),
			DrainTimeout: "15000",
		}
		ep.Name = hashIP(ep.IP)

		drainTimeout, dok := event.Data["drainTimeout"]
		if dok {
			ep.DrainTimeout = drainTimeout.(string)
		}

		isUpForDrain := ehandler.CheckOnEvent(ep)
		if isUpForDrain {
			ehandler.saveEventToMap(ep, event)
		} else {
			addedForDrain := ehandler.DoOnEvent(ep)
			if addedForDrain {
				ehandler.saveEventToMap(ep, event)
				go ehandler.pollOnDrainResults(ep, event, cli)
				go ehandler.doOnDrainTimeout(ep, event, cli)
			} else {
				log.Infof("[Endpoint IP: %v, name: %v] Result: Drain not needed", ep.IP, ep.Name)
				//send reply
				return true, nil
			}
		}
	}
	return false, nil
}

func (ehandler *REventsHandler) saveEventToMap(ep *config.Endpoint, event *revents.Event) {
	ehandler.EventMu.Lock()
	ehandler.EventMap[ep.Name] = event
	ehandler.EventMu.Unlock()
}

func (ehandler *REventsHandler) readEventMap(ep *config.Endpoint) *revents.Event {
	ehandler.EventMu.RLock()
	defer ehandler.EventMu.RUnlock()
	return ehandler.EventMap[ep.Name]
}

func (ehandler *REventsHandler) removeFromEventMap(ep *config.Endpoint) {
	ehandler.EventMu.Lock()
	defer ehandler.EventMu.Unlock()
	delete(ehandler.EventMap, ep.Name)
}

func (ehandler *REventsHandler) replyToEvent(ep *config.Endpoint, event *revents.Event, cli *client.RancherClient) error {
	//get the latest event from EventMap
	savedEvent := ehandler.readEventMap(ep)
	if savedEvent != nil {
		if err := ehandler.CreateAndPublishReply(savedEvent, cli); err != nil {
			return fmt.Errorf("Error replying to the event: %#v", err)
		}
		ehandler.removeFromEventMap(ep)
	} else {
		if err := ehandler.CreateAndPublishReply(event, cli); err != nil {
			return fmt.Errorf("Error replying to the event: %#v", err)
		}
	}
	return nil
}

func (ehandler *REventsHandler) pollOnDrainResults(ep *config.Endpoint, event *revents.Event, cli *client.RancherClient) {
	for {
		isUpForDrain := ehandler.CheckOnEvent(ep)
		if !isUpForDrain {
			log.Debugf("[Endpoint IP: %v, name: %v] is not in drainList anymore, stopping poll", ep.IP, ep.Name)
			if err := ehandler.CreateAndPublishReply(event, cli); err != nil {
				log.Errorf("Error replying to the event: %#v", err)
			}
			break
		}

		drained := ehandler.PollStatus(ep)
		log.Debugf("Check ep %v is drained: %v", ep.Name, drained)

		if drained {
			log.Infof("[Endpoint IP: %v, name: %v] Result: Drain complete, stopping poll", ep.IP, ep.Name)
			ehandler.DoOnTimeout(ep)
			err := ehandler.replyToEvent(ep, event, cli)
			if err != nil {
				log.Errorf("Error: %v", err)
			}
			break
		}
		time.Sleep(time.Duration(2) * time.Second)
	}
}

func (ehandler *REventsHandler) doOnDrainTimeout(ep *config.Endpoint, event *revents.Event, cli *client.RancherClient) {
	drainTime, err := time.ParseDuration(ep.DrainTimeout + "ms")
	if err != nil {
		log.Infof("Error %v parsing drainTimeout %v", err, ep.DrainTimeout)
		return
	}

	ticker := time.NewTicker(drainTime)
	for t := range ticker.C {
		log.Debugf("Tick to check DrainTimeout for endpoint %v, Tick at %v", ep.Name, t)
		isUpForDrain := ehandler.CheckOnEvent(ep)
		if !isUpForDrain {
			log.Debugf("[Endpoint IP: %v, name: %v] is not in drainList anymore, should have finished draining earlier", ep.IP, ep.Name)
			if err := ehandler.CreateAndPublishReply(event, cli); err != nil {
				log.Errorf("Error replying to the event: %#v", err)
			}
			break
		}
		log.Infof("[Endpoint IP: %v, name: %v] Result: DrainTimeout hit", ep.IP, ep.Name)
		//check if drained, if not remove from drain, reply to the event
		drained := ehandler.PollStatus(ep)
		if !drained {
			log.Debugf("[Endpoint IP: %v, name: %v] Not drained yet, but stopping draining", ep.IP, ep.Name)
			ehandler.DoOnTimeout(ep)
		}

		err := ehandler.replyToEvent(ep, event, cli)
		if err != nil {
			log.Errorf("Error: %v", err)
		}
		ticker.Stop()
	}
}
