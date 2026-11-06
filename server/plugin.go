package main

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/mattermost/mattermost/server/public/plugin"
)

const pluginID = "com.attoktist.scheduled-posts"

type Plugin struct {
	plugin.MattermostPlugin
	configuration atomic.Value
	gate          postGate
	kv            kvAPI
	post          poster
	status        statusAPI

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func (p *Plugin) OnActivate() error {
	p.gate, p.kv, p.post, p.status = p.API, p.API, p.API, p.API
	if err := p.API.RegisterCommand(p.scheduleCommand()); err != nil {
		return err
	}
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	go p.runScheduler()
	return nil
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.handleHTTP(w, r)
}

func (p *Plugin) OnDeactivate() error {
	p.stopOnce.Do(func() {
		if p.stopCh != nil {
			close(p.stopCh)
		}
	})
	if p.doneCh != nil {
		<-p.doneCh
	}
	return nil
}

func (p *Plugin) getConfiguration() *configuration {
	loaded := p.configuration.Load()
	if loaded == nil {
		return &configuration{
			PollIntervalSeconds: 30,
			MaxAttempts:         3,
			DefaultTimezone:     "UTC",
		}
	}
	return loaded.(*configuration)
}

func (p *Plugin) setConfiguration(cfg *configuration) {
	p.configuration.Store(cfg)
}
