package rancher

import (
	"github.com/Sirupsen/logrus"
	revents "github.com/rancher/event-subscriber/events"
	"github.com/rancher/go-rancher/v2"
)

type EventsHandler interface {
	Subscribe() error
}

type REventsHandler struct {
	CattleURL       string
	CattleAccessKey string
	CattleSecretKey string
}

func (ehandler *REventsHandler) Subscribe() error {

	eventHandlers := map[string]revents.EventHandler{
		"target.drain": ehandler.handle,
		"ping":         ehandler.handle,
	}

	router, err := revents.NewEventRouter("", 0, ehandler.CattleURL, ehandler.CattleAccessKey, ehandler.CattleSecretKey, nil, eventHandlers, "", 3, revents.DefaultPingConfig)
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
	if err := ehandler.CreateAndPublishReply(event, cli); err != nil {
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
