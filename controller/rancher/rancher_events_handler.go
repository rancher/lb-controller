package rancher

import (
	"github.com/Sirupsen/logrus"
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
	DoOnEvent       func(*config.Endpoint)
	CheckOnEvent    func(*config.Endpoint) bool
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
	err = router.StartWithoutCreate(nil)
	return err
}

func (ehandler *REventsHandler) handle(event *revents.Event, cli *client.RancherClient) error {
	log := logrus.WithFields(logrus.Fields{
		"eventName":  event.Name,
		"eventID":    event.ID,
		"resourceID": event.ResourceID,
	})
	log.Infof("Rancher event: %#v", event)

	if event.Name == "target.drain" {
		sendReply, err := ehandler.HandleDrainEvent(event, cli)
		if err != nil {
			log.Errorf("Error handling drain event: %#v", err)
			return err
		}
		if !sendReply {
			return nil
		}
	}

	if err := ehandler.CreateAndPublishReply(event, cli); err != nil {
		log.Errorf("Error replying to the event: %#v", err)
		return err
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
	logrus.Infof("New reply created to the event: %#v", reply)

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

	logrus.Infof("Received target.drain primaryIP: %v, targetPort: %v", event.Data["primaryIpAddress"], event.Data["targetPort"])

	primaryIP, ok := event.Data["targetIPaddress"]

	if ok {
		ep := &config.Endpoint{
			IP: primaryIP.(string),
		}
		ep.Name = hashIP(ep.IP)

		drained := ehandler.CheckOnEvent(ep)
		logrus.Infof("Check ep %v is drained: %v", ep.Name, drained)

		if !drained {
			ehandler.DoOnEvent(ep)
		} else {
			return true, nil
		}
	}
	return false, nil
}
