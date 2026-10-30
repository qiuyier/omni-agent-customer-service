package main

import (
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
)

func managedStore(repository conversation.Repository) (manage.Repository, error) {
	provider, ok := repository.(interface {
		KnowledgeStore() (manage.Repository, error)
	})
	if !ok {
		return nil, errors.New("managed knowledge requires a PostgreSQL knowledge store")
	}
	store, e := provider.KnowledgeStore()
	if e != nil || store == nil {
		return nil, errors.New("managed knowledge store unavailable")
	}
	return store, nil
}
